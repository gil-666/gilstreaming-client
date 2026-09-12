#pragma once

#include <QObject>
#include <QNetworkAccessManager>

class AutoUpdateChecker : public QObject
{
    Q_OBJECT
    Q_PROPERTY(bool checking READ checking NOTIFY checkingChanged)
public:
    explicit AutoUpdateChecker(QObject *parent = nullptr);

    Q_INVOKABLE void start();
    bool checking() const { return m_Checking; }

signals:
    void onUpdateAvailable(QString newVersion, QString url);
    void onUpdateCheckFinished(bool updateAvailable, QString message);
    void checkingChanged();

private slots:
    void handleUpdateCheckRequestFinished(QNetworkReply* reply);

private:
    void parseStringToVersionQuad(QString& string, QVector<int>& version);

    int compareVersion(QVector<int>& version1, QVector<int>& version2);

    QString getPlatform();
    void finishCheck(bool updateAvailable, const QString& message);

    QVector<int> m_CurrentVersionQuad;
    QNetworkAccessManager* m_Nam;
    bool m_Checking;
};
