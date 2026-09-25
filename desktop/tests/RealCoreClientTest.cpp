#include "core/RealCoreClient.h"

#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QDir>
#include <QLocalServer>
#include <QSignalSpy>
#include <QTest>

class IpcFixture final : public QObject
{
    Q_OBJECT

public:
    explicit IpcFixture(const QString &path, QObject *parent = nullptr)
        : QObject(parent), m_path(path)
    {
        QLocalServer::removeServer(m_path);
        if (!m_server.listen(m_path)) qFatal("cannot listen on test socket: %s", qPrintable(m_server.errorString()));
        connect(&m_server, &QLocalServer::newConnection, this, [this]() {
            m_socket = m_server.nextPendingConnection();
            connect(m_socket, &QLocalSocket::readyRead, this, &IpcFixture::readRequests);
        });
    }

    ~IpcFixture() override
    {
        if (m_socket) m_socket->disconnectFromServer();
        m_server.close();
        QLocalServer::removeServer(m_path);
    }

    int requestCount() const { return m_requestCount; }
    void setEmitRevisionGap(bool enabled) { m_emitRevisionGap = enabled; }

private slots:
    void readRequests()
    {
        m_buffer += m_socket->readAll();
        while (m_buffer.size() >= 4) {
            const quint32 size = (static_cast<quint8>(m_buffer[0]) << 24) |
                                 (static_cast<quint8>(m_buffer[1]) << 16) |
                                 (static_cast<quint8>(m_buffer[2]) << 8) |
                                 static_cast<quint8>(m_buffer[3]);
            if (size == 0 || m_buffer.size() < 4 + static_cast<int>(size)) return;
            const QJsonObject request = QJsonDocument::fromJson(m_buffer.mid(4, size)).object();
            m_buffer.remove(0, 4 + size);
            ++m_requestCount;
            respond(request);
        }
    }

private:
    void respond(const QJsonObject &request)
    {
        const QString method = request.value(QStringLiteral("method")).toString();
        QJsonObject response{{QStringLiteral("version"), 1},
                             {QStringLiteral("id"), request.value(QStringLiteral("id"))},
                             {QStringLiteral("ok"), true}};
        if (method == QStringLiteral("Handshake")) {
            response.insert(QStringLiteral("capabilities"), QJsonArray{QStringLiteral("Subscribe")});
        } else if (method == QStringLiteral("RetryRole")) {
            response.insert(QStringLiteral("ok"), false);
            response.insert(QStringLiteral("error_detail"), QJsonObject{
                {QStringLiteral("code"), QStringLiteral("validation_failed")},
                {QStringLiteral("message"), QStringLiteral("role validation failed")},
                {QStringLiteral("retryable"), true}});
        } else {
            QJsonObject session{{QStringLiteral("connected"), true},
                                {QStringLiteral("rx_bytes"), 12},
                                {QStringLiteral("tx_bytes"), 34},
                                {QStringLiteral("endpoint"), QStringLiteral("vpn.example:443")}};
            QJsonObject role{{QStringLiteral("id"), QStringLiteral("office")},
                             {QStringLiteral("label"), QStringLiteral("Office")},
                             {QStringLiteral("protocol"), QStringLiteral("openconnect")},
                             {QStringLiteral("state"), QStringLiteral("online")},
                             {QStringLiteral("interface"), QJsonObject{{QStringLiteral("name"), QStringLiteral("kk0")},
                                                                         {QStringLiteral("ifindex"), 8},
                                                                         {QStringLiteral("mtu"), 1380}}},
                             {QStringLiteral("session"), session},
                             {QStringLiteral("available_actions"), QJsonArray{QStringLiteral("disconnect"), QStringLiteral("retry")}}};
            const int revision = m_emitRevisionGap &&
                                         (method == QStringLiteral("Subscribe") ||
                                          request.value(QStringLiteral("id")).toString() == QStringLiteral("resync"))
                                     ? 3
                                     : 1;
            response.insert(QStringLiteral("snapshot"), QJsonObject{
                {QStringLiteral("schema"), 1},
                {QStringLiteral("revision"), revision},
                {QStringLiteral("core_state"), QStringLiteral("Ready")},
                {QStringLiteral("aggregate_state"), QStringLiteral("Ready")},
                {QStringLiteral("active_profile"), QStringLiteral("default")},
                {QStringLiteral("underlay_summary"), QStringLiteral("Wi-Fi")},
                {QStringLiteral("leshy_supported"), false},
                {QStringLiteral("roles"), QJsonArray{role}}
            });
        }
        const QByteArray payload = QJsonDocument(response).toJson(QJsonDocument::Compact);
        QByteArray frame(4, Qt::Uninitialized);
        const quint32 size = static_cast<quint32>(payload.size());
        frame[0] = static_cast<char>((size >> 24) & 0xff);
        frame[1] = static_cast<char>((size >> 16) & 0xff);
        frame[2] = static_cast<char>((size >> 8) & 0xff);
        frame[3] = static_cast<char>(size & 0xff);
        m_socket->write(frame + payload);
    }

    QString m_path;
    QLocalServer m_server;
    QLocalSocket *m_socket = nullptr;
    QByteArray m_buffer;
    int m_requestCount = 0;
    bool m_emitRevisionGap = false;
};

class RealCoreClientTest final : public QObject
{
    Q_OBJECT

private slots:
    void mapsSnapshotAndUsesLengthPrefixedFrames()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-test-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(1), 1000);
        QCOMPARE(client.coreState(), QStringLiteral("Ready"));
        QCOMPARE(client.aggregateState(), QStringLiteral("Ready"));
        QCOMPARE(client.activeProfile(), QStringLiteral("default"));
        QCOMPARE(client.roles()->rowCount(), 1);
        const RoleSnapshot role = client.roles()->roleAt(0);
        QCOMPARE(role.id, QStringLiteral("office"));
        QCOMPARE(role.protocol, QStringLiteral("openconnect"));
        QCOMPARE(role.state, QStringLiteral("Ready"));
        QCOMPARE(role.interfaceName, QStringLiteral("kk0"));
        QCOMPARE(role.interfaceIndex, 8);
        QCOMPARE(role.sessionEndpoint, QStringLiteral("vpn.example:443"));
        QCOMPARE(role.availableActions, QStringList({QStringLiteral("disconnect"), QStringLiteral("retry")}));
        QVERIFY(fixture.requestCount() >= 3);
    }

    void preservesSubscribedStateOnStructuredCommandError()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-error-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        RealCoreClient client(path, nullptr);
        QTRY_COMPARE_WITH_TIMEOUT(client.roles()->rowCount(), 1, 1000);
        client.retryRole(0);
        QTRY_COMPARE_WITH_TIMEOUT(client.lastCommandError(), QStringLiteral("role validation failed"), 1000);
        QCOMPARE(client.aggregateState(), QStringLiteral("Ready"));
        QCOMPARE(client.revision(), static_cast<qulonglong>(1));
    }

    void resynchronizesAfterRevisionGap()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-gap-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setEmitRevisionGap(true);
        RealCoreClient client(path, nullptr);
        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(3), 1000);
        QVERIFY(fixture.requestCount() >= 4);
    }
};

QTEST_MAIN(RealCoreClientTest)
#include "RealCoreClientTest.moc"
