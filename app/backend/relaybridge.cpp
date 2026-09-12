#include "relaybridge.h"

#include <QAbstractSocket>
#include <QDebug>
#include <QHostAddress>
#include <QNetworkRequest>
#include <QNetworkDatagram>
#include <QQueue>
#include <QTcpServer>
#include <QTcpSocket>
#include <QTimer>
#include <QUdpSocket>
#include <QUrlQuery>
#include <QWebSocket>

#include <utility>

namespace {
constexpr int TCP_OFFSETS[] = {-5, 0, 6, 7, 21};
constexpr int UDP_OFFSETS[] = {9, 10, 11, 13, 21};
constexpr qsizetype MAX_PENDING_TCP_BYTES = 1024 * 1024;
constexpr int MAX_PENDING_UDP_DATAGRAMS = 256;
}

class RelayBridge::TcpEndpoint : public QObject
{
public:
    TcpEndpoint(int portOffset, QObject* parent)
        : QObject(parent), offset(portOffset), server(new QTcpServer(this)) {}

    int offset;
    QTcpServer* server;
};

class RelayBridge::TcpTunnel : public QObject
{
public:
    explicit TcpTunnel(QObject* parent) : QObject(parent) {}

    QTcpSocket* local = nullptr;
    QWebSocket* websocket = nullptr;
    QByteArray pending;
    bool closing = false;
};

class RelayBridge::UdpEndpoint : public QObject
{
public:
    UdpEndpoint(int portOffset, QObject* parent)
        : QObject(parent), offset(portOffset), socket(new QUdpSocket(this)) {}

    int offset;
    QUdpSocket* socket;
    QWebSocket* websocket = nullptr;
    QHostAddress localPeerAddress;
    quint16 localPeerPort = 0;
    QQueue<QByteArray> pending;
};

RelayBridge::RelayBridge(QObject* parent)
    : QObject(parent), m_Running(false)
{
}

RelayBridge::~RelayBridge()
{
    stop();
}

bool RelayBridge::start(const QUrl& relayUrl, const QString& accessToken,
                        const QString& leaseId, int basePort, QString* errorMessage)
{
    stop();
    if (!relayUrl.isValid() || (relayUrl.scheme() != "wss" && relayUrl.scheme() != "ws") ||
            accessToken.isEmpty() || leaseId.isEmpty()) {
        if (errorMessage != nullptr) {
            *errorMessage = tr("The coordinator returned an invalid relay configuration.");
        }
        return false;
    }
    if (basePort - 5 < 1 || basePort + 21 > 65535) {
        if (errorMessage != nullptr) {
            *errorMessage = tr("The assigned relay port range is invalid.");
        }
        return false;
    }

    m_RelayUrl = relayUrl;
    m_AccessToken = accessToken;
    m_LeaseId = leaseId;
    m_Running = true;

    for (int offset : TCP_OFFSETS) {
        TcpEndpoint* endpoint = new TcpEndpoint(offset, this);
        if (!endpoint->server->listen(QHostAddress::LocalHost,
                                      static_cast<quint16>(basePort + offset))) {
            const QString details = endpoint->server->errorString();
            delete endpoint;
            if (errorMessage != nullptr) {
                *errorMessage = tr("Could not open the local relay port %1: %2")
                        .arg(basePort + offset).arg(details);
            }
            stop();
            return false;
        }
        connect(endpoint->server, &QTcpServer::newConnection, this,
                [this, endpoint]() { acceptTcp(endpoint); });
        m_TcpEndpoints.append(endpoint);
    }

    for (int offset : UDP_OFFSETS) {
        UdpEndpoint* endpoint = new UdpEndpoint(offset, this);
        if (!endpoint->socket->bind(QHostAddress::LocalHost,
                                    static_cast<quint16>(basePort + offset))) {
            const QString details = endpoint->socket->errorString();
            delete endpoint;
            if (errorMessage != nullptr) {
                *errorMessage = tr("Could not open the local relay port %1: %2")
                        .arg(basePort + offset).arg(details);
            }
            stop();
            return false;
        }
        connect(endpoint->socket, &QUdpSocket::readyRead, this, [this, endpoint]() {
            while (endpoint->socket->hasPendingDatagrams()) {
                QNetworkDatagram datagram = endpoint->socket->receiveDatagram();
                endpoint->localPeerAddress = datagram.senderAddress();
                endpoint->localPeerPort = datagram.senderPort();
                if (endpoint->websocket != nullptr &&
                        endpoint->websocket->state() == QAbstractSocket::ConnectedState) {
                    endpoint->websocket->sendBinaryMessage(datagram.data());
                }
                else {
                    if (endpoint->pending.size() == MAX_PENDING_UDP_DATAGRAMS) {
                        endpoint->pending.dequeue();
                    }
                    endpoint->pending.enqueue(datagram.data());
                    if (endpoint->websocket == nullptr) {
                        openUdpTunnel(endpoint);
                    }
                }
            }
        });
        m_UdpEndpoints.append(endpoint);
    }

    qInfo() << "Local Sunshine relay bridge listening on 127.0.0.1 with base port" << basePort;
    return true;
}

void RelayBridge::stop()
{
    m_Running = false;

    const QList<TcpTunnel*> tunnels = m_TcpTunnels;
    for (TcpTunnel* tunnel : tunnels) {
        closeTcpTunnel(tunnel);
    }
    m_TcpTunnels.clear();

    for (TcpEndpoint* endpoint : std::as_const(m_TcpEndpoints)) {
        endpoint->server->close();
        delete endpoint;
    }
    m_TcpEndpoints.clear();

    for (UdpEndpoint* endpoint : std::as_const(m_UdpEndpoints)) {
        endpoint->socket->close();
        if (endpoint->websocket != nullptr) {
            endpoint->websocket->abort();
        }
        delete endpoint;
    }
    m_UdpEndpoints.clear();

    m_RelayUrl.clear();
    m_AccessToken.clear();
    m_LeaseId.clear();
}

QNetworkRequest RelayBridge::relayRequest(const char* transport, int offset) const
{
    QUrl url = m_RelayUrl;
    QUrlQuery query(url);
    query.addQueryItem("leaseId", m_LeaseId);
    query.addQueryItem("transport", QString::fromLatin1(transport));
    query.addQueryItem("offset", QString::number(offset));
    url.setQuery(query);

    QNetworkRequest request(url);
    request.setRawHeader("Authorization", "Bearer " + m_AccessToken.toUtf8());
    request.setRawHeader("User-Agent", "GilStreaming relay");
    return request;
}

void RelayBridge::acceptTcp(TcpEndpoint* endpoint)
{
    while (endpoint->server->hasPendingConnections()) {
        openTcpTunnel(endpoint, endpoint->server->nextPendingConnection());
    }
}

void RelayBridge::openTcpTunnel(TcpEndpoint* endpoint, QTcpSocket* localSocket)
{
    TcpTunnel* tunnel = new TcpTunnel(this);
    tunnel->local = localSocket;
    localSocket->setParent(tunnel);
    tunnel->websocket = new QWebSocket(QString(), QWebSocketProtocol::VersionLatest, tunnel);
    m_TcpTunnels.append(tunnel);

    connect(localSocket, &QTcpSocket::readyRead, tunnel, [this, tunnel]() {
        const QByteArray payload = tunnel->local->readAll();
        if (tunnel->websocket->state() == QAbstractSocket::ConnectedState) {
            tunnel->websocket->sendBinaryMessage(payload);
        }
        else if (tunnel->pending.size() + payload.size() <= MAX_PENDING_TCP_BYTES) {
            tunnel->pending.append(payload);
        }
        else {
            qWarning() << "Closing relay TCP channel because its pending buffer is full";
            closeTcpTunnel(tunnel);
        }
    });
    connect(localSocket, &QTcpSocket::disconnected, tunnel,
            [this, tunnel]() { closeTcpTunnel(tunnel); });
    connect(tunnel->websocket, &QWebSocket::connected, tunnel, [tunnel]() {
        if (!tunnel->pending.isEmpty()) {
            tunnel->websocket->sendBinaryMessage(tunnel->pending);
            tunnel->pending.clear();
        }
    });
    connect(tunnel->websocket, &QWebSocket::binaryMessageReceived, tunnel,
            [tunnel](const QByteArray& payload) {
        if (tunnel->local->state() == QAbstractSocket::ConnectedState) {
            tunnel->local->write(payload);
        }
    });
    connect(tunnel->websocket, &QWebSocket::disconnected, tunnel,
            [this, tunnel]() { closeTcpTunnel(tunnel); });
#if QT_VERSION >= QT_VERSION_CHECK(6, 0, 0)
    connect(tunnel->websocket, &QWebSocket::errorOccurred, tunnel,
            [tunnel](QAbstractSocket::SocketError) {
        qWarning() << "Relay TCP WebSocket error:" << tunnel->websocket->errorString();
    });
#else
    connect(tunnel->websocket,
            QOverload<QAbstractSocket::SocketError>::of(&QWebSocket::error), tunnel,
            [tunnel](QAbstractSocket::SocketError) {
        qWarning() << "Relay TCP WebSocket error:" << tunnel->websocket->errorString();
    });
#endif
    tunnel->websocket->open(relayRequest("tcp", endpoint->offset));
}

void RelayBridge::closeTcpTunnel(TcpTunnel* tunnel)
{
    if (tunnel == nullptr || tunnel->closing) {
        return;
    }
    tunnel->closing = true;
    m_TcpTunnels.removeOne(tunnel);
    tunnel->local->abort();
    tunnel->websocket->abort();
    tunnel->deleteLater();
}

void RelayBridge::openUdpTunnel(UdpEndpoint* endpoint)
{
    if (!m_Running || endpoint->websocket != nullptr) {
        return;
    }

    QWebSocket* websocket = new QWebSocket(QString(), QWebSocketProtocol::VersionLatest, endpoint);
    endpoint->websocket = websocket;
    connect(websocket, &QWebSocket::connected, endpoint, [endpoint, websocket]() {
        while (endpoint->websocket == websocket && !endpoint->pending.isEmpty()) {
            websocket->sendBinaryMessage(endpoint->pending.dequeue());
        }
    });
    connect(websocket, &QWebSocket::binaryMessageReceived, endpoint,
            [endpoint, websocket](const QByteArray& payload) {
        if (endpoint->websocket == websocket && endpoint->localPeerPort != 0) {
            endpoint->socket->writeDatagram(payload, endpoint->localPeerAddress,
                                            endpoint->localPeerPort);
        }
    });
    connect(websocket, &QWebSocket::disconnected, endpoint, [this, endpoint, websocket]() {
        if (endpoint->websocket != websocket) {
            return;
        }
        endpoint->websocket = nullptr;
        websocket->deleteLater();
        if (m_Running && !endpoint->pending.isEmpty()) {
            QTimer::singleShot(500, endpoint, [this, endpoint]() { openUdpTunnel(endpoint); });
        }
    });
#if QT_VERSION >= QT_VERSION_CHECK(6, 0, 0)
    connect(websocket, &QWebSocket::errorOccurred, endpoint,
            [websocket](QAbstractSocket::SocketError) {
        qWarning() << "Relay UDP WebSocket error:" << websocket->errorString();
    });
#else
    connect(websocket, QOverload<QAbstractSocket::SocketError>::of(&QWebSocket::error), endpoint,
            [websocket](QAbstractSocket::SocketError) {
        qWarning() << "Relay UDP WebSocket error:" << websocket->errorString();
    });
#endif
    websocket->open(relayRequest("udp", endpoint->offset));
}
