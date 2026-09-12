#pragma once

#include <QObject>
#include <QTimer>
#include <QUrl>

class QNetworkRequest;

class RelayBridge : public QObject
{
    Q_OBJECT

public:
    explicit RelayBridge(QObject* parent = nullptr);
    ~RelayBridge() override;

    bool start(const QUrl& relayUrl, const QString& accessToken,
               const QString& leaseId, int basePort, QString* errorMessage);
    void stop();
    bool isRunning() const { return m_Running; }

private:
    class TcpEndpoint;
    class TcpTunnel;
    class UdpEndpoint;

    void acceptTcp(TcpEndpoint* endpoint);
    void openTcpTunnel(TcpEndpoint* endpoint, class QTcpSocket* localSocket);
    void closeTcpTunnel(TcpTunnel* tunnel);
    void openUdpTunnel(UdpEndpoint* endpoint);
    QNetworkRequest relayRequest(const char* transport, int offset) const;

    QList<TcpEndpoint*> m_TcpEndpoints;
    QList<TcpTunnel*> m_TcpTunnels;
    QList<UdpEndpoint*> m_UdpEndpoints;
    QUrl m_RelayUrl;
    QTimer m_KeepaliveTimer;
    QString m_AccessToken;
    QString m_LeaseId;
    bool m_Running;
};
