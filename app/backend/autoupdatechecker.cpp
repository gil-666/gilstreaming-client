#include "autoupdatechecker.h"

#include <QNetworkReply>
#include <QJsonDocument>
#include <QJsonArray>
#include <QJsonObject>

AutoUpdateChecker::AutoUpdateChecker(QObject *parent) :
    QObject(parent),
    m_Nam(nullptr),
    m_Checking(false)
{
    QString currentVersion(VERSION_STR);
    qDebug() << "Current GilStreaming version:" << currentVersion;
    parseStringToVersionQuad(currentVersion, m_CurrentVersionQuad);

    // Should at least have a 1.0-style version number
    Q_ASSERT(m_CurrentVersionQuad.count() > 1);
}

void AutoUpdateChecker::start()
{
    if (m_Checking) {
        return;
    }

#if defined(Q_OS_WIN32) || defined(Q_OS_DARWIN) || defined(STEAM_LINK) || defined(APP_IMAGE) // Only run update checker on platforms without auto-update

    m_Nam = new QNetworkAccessManager(this);
    m_Nam->setStrictTransportSecurityEnabled(true);
    m_Nam->setRedirectPolicy(QNetworkRequest::NoLessSafeRedirectPolicy);
    connect(m_Nam, &QNetworkAccessManager::finished,
            this, &AutoUpdateChecker::handleUpdateCheckRequestFinished);
    m_Checking = true;
    emit checkingChanged();

#if QT_VERSION >= QT_VERSION_CHECK(5, 14, 0) && QT_VERSION < QT_VERSION_CHECK(5, 15, 1) && !defined(QT_NO_BEARERMANAGEMENT)
    // HACK: Set network accessibility to work around QTBUG-80947 (introduced in Qt 5.14.0 and fixed in Qt 5.15.1)
    QT_WARNING_PUSH
    QT_WARNING_DISABLE_DEPRECATED
    m_Nam->setNetworkAccessible(QNetworkAccessManager::Accessible);
    QT_WARNING_POP
#endif

    // We'll get a callback when this is finished
    // The continuous release is replaced only after the full GitHub Actions
    // build succeeds. Release assets provide a stable, public download URL,
    // unlike short-lived Actions artifact URLs.
    QUrl url("https://github.com/gil-666/gilstreaming-client/releases/download/continuous/update.json");
    QNetworkRequest request(url);
#if QT_VERSION >= QT_VERSION_CHECK(5, 15, 0)
    request.setAttribute(QNetworkRequest::Http2AllowedAttribute, true);
#else
    request.setAttribute(QNetworkRequest::HTTP2AllowedAttribute, true);
#endif
    m_Nam->get(request);
#else
    emit onUpdateCheckFinished(false, tr("Updates are managed by your system package manager."));
#endif
}

void AutoUpdateChecker::finishCheck(bool updateAvailable, const QString& message)
{
    if (m_Nam) {
        m_Nam->deleteLater();
        m_Nam = nullptr;
    }
    if (m_Checking) {
        m_Checking = false;
        emit checkingChanged();
    }
    emit onUpdateCheckFinished(updateAvailable, message);
}

void AutoUpdateChecker::parseStringToVersionQuad(QString& string, QVector<int>& version)
{
    QStringList list = string.split('.');
    for (const QString& component : std::as_const(list)) {
        version.append(component.toInt());
    }
}

QString AutoUpdateChecker::getPlatform()
{
#if defined(STEAM_LINK)
    return QStringLiteral("steamlink");
#elif defined(APP_IMAGE)
    return QStringLiteral("appimage");
#elif defined(Q_OS_DARWIN) && QT_VERSION >= QT_VERSION_CHECK(6, 0, 0)
    // Qt 6 changed this from 'osx' to 'macos'. Use the old one
    // to be consistent (and not require another entry in the manifest).
    return QStringLiteral("osx");
#else
    return QSysInfo::productType();
#endif
}

int AutoUpdateChecker::compareVersion(QVector<int>& version1, QVector<int>& version2) {
    for (int i = 0;; i++) {
        int v1Val = 0;
        int v2Val = 0;

        // Treat missing decimal places as 0
        if (i < version1.count()) {
            v1Val = version1[i];
        }
        if (i < version2.count()) {
            v2Val = version2[i];
        }
        if (i >= version1.count() && i >= version2.count()) {
            // Equal versions
            return 0;
        }

        if (v1Val < v2Val) {
            return -1;
        }
        else if (v1Val > v2Val) {
            return 1;
        }
    }
}

void AutoUpdateChecker::handleUpdateCheckRequestFinished(QNetworkReply* reply)
{
    Q_ASSERT(reply->isFinished());

    if (reply->error() == QNetworkReply::NoError) {
        QTextStream stream(reply);

#if QT_VERSION >= QT_VERSION_CHECK(6, 0, 0)
        stream.setEncoding(QStringConverter::Utf8);
#else
        stream.setCodec("UTF-8");
#endif

        // Read all data and queue the reply for deletion
        QString jsonString = stream.readAll();
        reply->deleteLater();

        QJsonParseError error;
        QJsonDocument jsonDoc = QJsonDocument::fromJson(jsonString.toUtf8(), &error);
        if (jsonDoc.isNull()) {
            qWarning() << "Update manifest malformed:" << error.errorString();
            finishCheck(false, tr("Could not read the update information."));
            return;
        }

        QJsonArray array;
        if (jsonDoc.isArray()) {
            array = jsonDoc.array();
        }
        else if (jsonDoc.isObject()) {
            // Older continuous releases published a single object rather than
            // an array. Accept both formats so installed clients can update.
            array.append(jsonDoc.object());
        }
        if (array.isEmpty()) {
            qWarning() << "Update manifest doesn't contain an array";
            finishCheck(false, tr("The update information is empty."));
            return;
        }

        for (const auto& updateEntry : std::as_const(array)) {
            if (updateEntry.isObject()) {
                QJsonObject updateObj = updateEntry.toObject();
                if (!updateObj.contains("platform") ||
                        !updateObj.contains("arch") ||
                        !updateObj.contains("version") ||
                        !updateObj.contains("browser_url")) {
                    qWarning() << "Update manifest entry missing vital field";
                    continue;
                }

                if (!updateObj["platform"].isString() ||
                        !updateObj["arch"].isString() ||
                        !updateObj["version"].isString() ||
                        !updateObj["browser_url"].isString()) {
                    qWarning() << "Update manifest entry has unexpected vital field type";
                    continue;
                }

                if (updateObj["arch"] == QSysInfo::buildCpuArchitecture() &&
                        updateObj["platform"] == getPlatform()) {

                    // Check the kernel version minimum if one exists
                    if (updateObj.contains("kernel_version_at_least") && updateObj["kernel_version_at_least"].isString()) {
                        QVector<int> requiredVersionQuad;
                        QVector<int> actualVersionQuad;

                        QString requiredVersion = updateObj["kernel_version_at_least"].toString();
                        QString actualVersion = QSysInfo::kernelVersion();
                        parseStringToVersionQuad(requiredVersion, requiredVersionQuad);
                        parseStringToVersionQuad(actualVersion, actualVersionQuad);

                        if (compareVersion(actualVersionQuad, requiredVersionQuad) < 0) {
                            qDebug() << "Skipping manifest entry due to kernel version (" << actualVersion << "<" << requiredVersion << ")";
                            continue;
                        }
                    }

                    qDebug() << "Found update manifest match for current platform";

                    QString latestVersion = updateObj["version"].toString();
                    qDebug() << "Latest version of GilStreaming for this platform is:" << latestVersion;

                    QVector<int> latestVersionQuad;
                    parseStringToVersionQuad(latestVersion, latestVersionQuad);

                    int res = compareVersion(m_CurrentVersionQuad, latestVersionQuad);
                    if (res < 0) {
                        // m_CurrentVersionQuad < latestVersionQuad
                        qDebug() << "Update available";
                        emit onUpdateAvailable(updateObj["version"].toString(),
                                               updateObj["browser_url"].toString());
                        finishCheck(true, tr("Version %1 is available.").arg(latestVersion));
                        return;
                    }
                    else if (res > 0) {
                        qDebug() << "Update manifest version lower than current version";
                        finishCheck(false, tr("You're up to date (version %1).").arg(QStringLiteral(VERSION_STR)));
                        return;
                    }
                    else {
                        qDebug() << "Update manifest version equal to current version";
                        finishCheck(false, tr("You're up to date (version %1).").arg(QStringLiteral(VERSION_STR)));
                        return;
                    }
                }
            }
            else {
                qWarning() << "Update manifest contained unrecognized entry:" << updateEntry.toString();
            }
        }

        qWarning() << "No entry in update manifest found for current platform:"
                   << QSysInfo::buildCpuArchitecture() << getPlatform() << QSysInfo::kernelVersion();
        finishCheck(false, tr("No update package is available for this platform."));
    }
    else {
        qWarning() << "Update checking failed with error:" << reply->error();
        const QString errorMessage = reply->errorString();
        reply->deleteLater();
        finishCheck(false, tr("Could not check for updates: %1").arg(errorMessage));
    }
}
