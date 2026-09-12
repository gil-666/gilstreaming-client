#include "relaybridge.h"

#include <QAbstractSocket>
#include <QCoreApplication>
#include <QDebug>
#include <QElapsedTimer>
#include <QFileInfo>
#include <QHostAddress>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QNetworkRequest>
#include <QNetworkDatagram>
#include <QProcess>
#include <QQueue>
#include <QStringList>
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
    m_KeepaliveTimer.setInterval(20000);
    connect(&m_KeepaliveTimer, &QTimer::timeout, this, [this]() {
        const QList<QWebSocket*> sockets = findChildren<QWebSocket*>();
        for (QWebSocket* socket : sockets) {
            if (socket->state() == QAbstractSocket::ConnectedState) {
                socket->ping();
            }
        }
    });
}

RelayBridge::~RelayBridge()
{
    stop();
}

bool RelayBridge::start(const QUrl& relayUrl, const QString& accessToken,
                        const QString& leaseId, int basePort, const QJsonObject& turn,
                        QString* errorMessage)
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
    m_BasePort = basePort;
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

    QString turnError;
    if (!startTurnHelper(turn, basePort, &turnError)) {
        if (!turn.isEmpty()) {
            qWarning() << "Native TURN/UDP unavailable; using WebSocket UDP fallback:" << turnError;
        }
        if (!startWebSocketUdpFallback(basePort, errorMessage)) {
            stop();
            return false;
        }
    }

    qInfo() << "Local Sunshine relay bridge listening on 127.0.0.1 with base port" << basePort;
    m_KeepaliveTimer.start();
    return true;
}

bool RelayBridge::startWebSocketUdpFallback(int basePort, QString* errorMessage)
{
    if (!m_UdpEndpoints.isEmpty()) {
        return true;
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
            for (UdpEndpoint* existing : std::as_const(m_UdpEndpoints)) {
                existing->socket->close();
                delete existing;
            }
            m_UdpEndpoints.clear();
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
    return true;
}

bool RelayBridge::startTurnHelper(const QJsonObject& turn, int basePort, QString* errorMessage)
{
    const QString server = turn.value("server").toString();
    const int port = turn.value("port").toInt();
    const QString username = turn.value("username").toString();
    const QString credential = turn.value("credential").toString();
    const QString peerAddress = turn.value("peerAddress").toString();
    const int peerBasePort = turn.value("peerBasePort").toInt();
    if (server.isEmpty() || port < 1 || port > 65535 || username.isEmpty() ||
            credential.isEmpty() || peerAddress.isEmpty() || peerBasePort < 1 ||
            peerBasePort + 21 > 65535) {
        if (errorMessage != nullptr) {
            *errorMessage = tr("The coordinator did not provide usable TURN credentials.");
        }
        return false;
    }

    QList<int> candidatePorts;
    for (const QJsonValue& value : turn.value("ports").toArray()) {
        const int candidatePort = value.toInt();
        if (candidatePort >= 1 && candidatePort <= 65535 &&
                !candidatePorts.contains(candidatePort)) {
            candidatePorts.append(candidatePort);
        }
    }
    if (!candidatePorts.contains(port)) {
        candidatePorts.prepend(port);
    }

#ifdef Q_OS_WIN
    const QString helperName = QStringLiteral("GilStreamingTurnRelay.exe");
#else
    const QString helperName = QStringLiteral("gilstreaming-turn-relay");
#endif
    const QString helperPath = QCoreApplication::applicationDirPath() + QLatin1Char('/') + helperName;
    if (!QFileInfo::exists(helperPath)) {
        if (errorMessage != nullptr) {
            *errorMessage = tr("The bundled TURN helper is missing.");
        }
        return false;
    }

    QStringList failures;
    for (int candidatePort : std::as_const(candidatePorts)) {
        QProcess* process = new QProcess(this);
        process->setProgram(helperPath);
        process->setProcessChannelMode(QProcess::SeparateChannels);
        process->start(QIODevice::ReadWrite);
        if (!process->waitForStarted(3000)) {
            failures.append(tr("UDP %1: helper did not start (%2)")
                            .arg(candidatePort).arg(process->errorString()));
            delete process;
            continue;
        }

        QJsonObject config{
            {"serverAddress", server + QLatin1Char(':') + QString::number(candidatePort)},
            {"username", username},
            {"credential", credential},
            {"peerAddress", peerAddress},
            {"peerBasePort", peerBasePort},
            {"localBasePort", basePort}
        };
        process->write(QJsonDocument(config).toJson(QJsonDocument::Compact));
        process->write("\n");
        process->closeWriteChannel();

        QByteArray output;
        QElapsedTimer timer;
        timer.start();
        while (timer.elapsed() < 10000 && process->state() != QProcess::NotRunning &&
               !output.contains('\n')) {
            process->waitForReadyRead(qMin(500, 10000 - static_cast<int>(timer.elapsed())));
            output.append(process->readAllStandardOutput());
        }
        output.append(process->readAllStandardOutput());
        if (!output.startsWith("READY ")) {
            const QString details = QString::fromUtf8(process->readAllStandardError()).trimmed();
            process->kill();
            process->waitForFinished(1000);
            failures.append(details.isEmpty()
                            ? tr("UDP %1: allocation timed out").arg(candidatePort)
                            : tr("UDP %1: %2").arg(candidatePort).arg(details));
            delete process;
            continue;
        }

        m_TurnProcess = process;
        connect(process, &QProcess::readyReadStandardError, this, [process]() {
            const QString details = QString::fromUtf8(process->readAllStandardError()).trimmed();
            if (!details.isEmpty()) {
                qInfo().noquote() << "TURN UDP:" << details;
            }
        });
        connect(process, QOverload<int, QProcess::ExitStatus>::of(&QProcess::finished), this,
                [this, process](int exitCode, QProcess::ExitStatus) {
            if (m_TurnProcess != process) {
                return;
            }
            m_TurnProcess = nullptr;
            qWarning() << "TURN UDP helper exited with code" << exitCode
                       << QString::fromUtf8(process->readAllStandardError()).trimmed();
            process->deleteLater();
            if (m_Running) {
                QString fallbackError;
                if (!startWebSocketUdpFallback(m_BasePort, &fallbackError)) {
                    qWarning() << "Could not activate WebSocket UDP fallback:" << fallbackError;
                    stop();
                }
            }
        });
        qInfo() << "Native TURN/UDP relay active on port" << candidatePort << ':'
                << QString::fromUtf8(output).trimmed();
        return true;
    }

    if (errorMessage != nullptr) {
        *errorMessage = tr("The TURN allocation failed: %1").arg(failures.join("; "));
    }
    return false;
}

void RelayBridge::stopTurnHelper()
{
    if (m_TurnProcess == nullptr) {
        return;
    }
    QProcess* process = m_TurnProcess;
    m_TurnProcess = nullptr;
    disconnect(process, nullptr, this, nullptr);
    process->terminate();
    if (!process->waitForFinished(1500)) {
        process->kill();
        process->waitForFinished(1000);
    }
    delete process;
}

void RelayBridge::stop()
{
    m_Running = false;
    m_KeepaliveTimer.stop();
    stopTurnHelper();

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
    m_BasePort = 0;
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
            // Sunshine closes each RTSP connection after its response. Push the
            // response into the loopback socket before the relay WebSocket's
            // close notification arrives.
            tunnel->local->flush();
        }
    });
    connect(tunnel->websocket, &QWebSocket::disconnected, tunnel,
            [this, tunnel]() {
        if (tunnel->closing) {
            return;
        }

        // An upstream EOF is meaningful to protocols such as RTSP, where the
        // peer closes the connection to delimit the response. Gracefully close
        // the loopback side so queued bytes are delivered before EOF. Using
        // abort() here can discard the response and surface as RTSP error -1.
        tunnel->closing = true;
        m_TcpTunnels.removeOne(tunnel);
        tunnel->local->disconnectFromHost();
        if (tunnel->local->state() == QAbstractSocket::UnconnectedState) {
            tunnel->deleteLater();
        }
        else {
            connect(tunnel->local, &QTcpSocket::disconnected,
                    tunnel, &QObject::deleteLater);
            QTimer::singleShot(2000, tunnel, [tunnel]() {
                if (tunnel->local->state() != QAbstractSocket::UnconnectedState) {
                    tunnel->local->abort();
                }
                tunnel->deleteLater();
            });
        }
    });
    connect(tunnel->websocket,
            QOverload<QAbstractSocket::SocketError>::of(&QWebSocket::error), tunnel,
            [tunnel](QAbstractSocket::SocketError) {
        qWarning() << "Relay TCP WebSocket error:" << tunnel->websocket->errorString();
    });
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
    connect(websocket, QOverload<QAbstractSocket::SocketError>::of(&QWebSocket::error), endpoint,
            [websocket](QAbstractSocket::SocketError) {
        qWarning() << "Relay UDP WebSocket error:" << websocket->errorString();
    });
    websocket->open(relayRequest("udp", endpoint->offset));
}
