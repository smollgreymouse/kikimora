#include "core/RealCoreClient.h"

#include <QFile>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QDir>
#include <QLocalServer>
#include <QSignalSpy>
#include <QTest>

#ifdef Q_OS_WIN
// The Windows client transport (CoreSocket) speaks AF_UNIX over Winsock
// directly, so the in-process fixture must listen the same way: a QLocalServer
// named pipe is a different socket family the client can never connect to.
#include <QSocketNotifier>
#include <winsock2.h>
#include <ws2tcpip.h>
#if __has_include(<afunix.h>)
#include <afunix.h>
#else
struct sockaddr_un {
    ADDRESS_FAMILY sun_family;
    char sun_path[108];
};
#endif
#endif

class IpcFixture final : public QObject
{
    Q_OBJECT

public:
    explicit IpcFixture(const QString &path, QObject *parent = nullptr)
        : QObject(parent), m_path(path)
    {
#ifdef Q_OS_WIN
        ensureWinsock();
        QFile::remove(m_path);
        m_listen = ::socket(AF_UNIX, SOCK_STREAM, 0);
        if (m_listen == INVALID_SOCKET) qFatal("cannot create AF_UNIX listener: %s", qPrintable(m_path));
        sockaddr_un address{};
        address.sun_family = AF_UNIX;
        const QByteArray encoded = QFile::encodeName(m_path);
        if (encoded.isEmpty() || encoded.size() >= int(sizeof(address.sun_path)))
            qFatal("test socket path too long: %s", qPrintable(m_path));
        memcpy(address.sun_path, encoded.constData(), size_t(encoded.size()) + 1);
        if (::bind(m_listen, reinterpret_cast<sockaddr *>(&address), sizeof(address)) == SOCKET_ERROR)
            qFatal("cannot bind test socket: %s", qPrintable(m_path));
        if (::listen(m_listen, 1) == SOCKET_ERROR)
            qFatal("cannot listen on test socket: %s", qPrintable(m_path));
        u_long nonBlocking = 1;
        ioctlsocket(m_listen, FIONBIO, &nonBlocking);
        m_acceptNotifier = new QSocketNotifier(m_listen, QSocketNotifier::Read, this);
        connect(m_acceptNotifier, &QSocketNotifier::activated, this, &IpcFixture::acceptClient);
#else
        QLocalServer::removeServer(m_path);
        if (!m_server.listen(m_path)) qFatal("cannot listen on test socket: %s", qPrintable(m_server.errorString()));
        connect(&m_server, &QLocalServer::newConnection, this, [this]() {
            m_socket = m_server.nextPendingConnection();
            connect(m_socket, &QLocalSocket::readyRead, this, &IpcFixture::readRequests);
        });
#endif
    }

    ~IpcFixture() override
    {
#ifdef Q_OS_WIN
        delete m_clientNotifier;
        delete m_acceptNotifier;
        if (m_client != INVALID_SOCKET) ::closesocket(m_client);
        if (m_listen != INVALID_SOCKET) ::closesocket(m_listen);
        QFile::remove(m_path);
#else
        if (m_socket) m_socket->disconnectFromServer();
        m_server.close();
        QLocalServer::removeServer(m_path);
#endif
    }

    int requestCount() const { return m_requestCount; }
    void setEmitRevisionGap(bool enabled) { m_emitRevisionGap = enabled; }
    bool receivedSetActiveProfile(const QString &name) const { return m_lastSetActiveProfile == name; }
    void setAggregateState(const QString &aggregate) { m_aggregateState = aggregate; }
    void setBypassMode(const QString &mode) { m_bypassMode = mode; }
    int routingListRequests() const { return m_routingListRequests; }
    QString lastMethod() const { return m_lastMethod; }

private slots:
    void readRequests()
    {
        m_buffer += readFromClient();
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
#ifdef Q_OS_WIN
    void acceptClient()
    {
        m_client = ::accept(m_listen, nullptr, nullptr);
        if (m_client == INVALID_SOCKET) return;
        m_acceptNotifier->setEnabled(false);
        u_long nonBlocking = 1;
        ioctlsocket(m_client, FIONBIO, &nonBlocking);
        m_clientNotifier = new QSocketNotifier(m_client, QSocketNotifier::Read, this);
        connect(m_clientNotifier, &QSocketNotifier::activated, this, &IpcFixture::readRequests);
    }

    QByteArray readFromClient()
    {
        QByteArray out;
        char buffer[64 * 1024];
        for (;;) {
            const int received = ::recv(m_client, buffer, sizeof(buffer), 0);
            if (received <= 0) break;
            out.append(buffer, received);
        }
        return out;
    }
#else
    QByteArray readFromClient()
    {
        return m_socket->readAll();
    }
#endif

    void sendToClient(const QByteArray &payload)
    {
#ifdef Q_OS_WIN
        if (m_client == INVALID_SOCKET) return;
        ::send(m_client, payload.constData(), int(payload.size()), 0);
#else
        m_socket->write(payload);
#endif
    }

#ifdef Q_OS_WIN
    void ensureWinsock()
    {
        static const bool ready = [] {
            WSADATA data;
            return WSAStartup(MAKEWORD(2, 2), &data) == 0;
        }();
        Q_UNUSED(ready);
    }
#endif

    void respond(const QJsonObject &request)
    {
        const QString method = request.value(QStringLiteral("method")).toString();
        m_lastMethod = method;
        if (method == QStringLiteral("SetActiveProfile")) {
            m_lastSetActiveProfile = request.value(QStringLiteral("profile")).toString();
        }
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
        } else if (method == QStringLiteral("GetRoutingLists")) {
            ++m_routingListRequests;
            response.insert(QStringLiteral("lists"), QJsonObject{});
            response.insert(QStringLiteral("zones"), QJsonArray{});
            response.insert(QStringLiteral("bypass_mode"), m_bypassMode);
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
                {QStringLiteral("aggregate_state"), m_aggregateState.isEmpty() ? QStringLiteral("Ready") : m_aggregateState},
                {QStringLiteral("active_profile"), QStringLiteral("default")},
                {QStringLiteral("profiles"), QJsonArray{QStringLiteral("default"), QStringLiteral("work"), QStringLiteral("home")}},
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
        sendToClient(frame + payload);
    }

    QString m_path;
#ifdef Q_OS_WIN
    SOCKET m_listen = INVALID_SOCKET;
    SOCKET m_client = INVALID_SOCKET;
    QSocketNotifier *m_acceptNotifier = nullptr;
    QSocketNotifier *m_clientNotifier = nullptr;
#else
    QLocalServer m_server;
    QLocalSocket *m_socket = nullptr;
#endif
    QByteArray m_buffer;
    int m_requestCount = 0;
    bool m_emitRevisionGap = false;
    QString m_lastSetActiveProfile;
    QString m_aggregateState;
    QString m_lastMethod;
    QString m_bypassMode = QStringLiteral("direct");
    int m_routingListRequests = 0;
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

    void exposesProfilesAndSwitchesActiveProfile()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-profiles-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(1), 1000);
        QCOMPARE(client.activeProfile(), QStringLiteral("default"));
        QCOMPARE(client.profiles(), QStringList({QStringLiteral("default"), QStringLiteral("work"), QStringLiteral("home")}));

        client.setActiveProfile(QStringLiteral("work"));
        QTRY_VERIFY_WITH_TIMEOUT(fixture.receivedSetActiveProfile(QStringLiteral("work")), 1000);
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

    // Regression for the stuck connect button: with live-but-partial tunnels
    // the label must stay honest (PartiallyReady is not "Not connected") and
    // the action must remain disconnect-all, because ConnectAll is a no-op on
    // the core when every role is already enabled.
    void labelsPartialAggregatesHonestlyAndStaysActionable()
    {
        const QString partialPath = QDir::temp().filePath(QStringLiteral("kikimora-core-partial-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(partialPath);
        fixture.setAggregateState(QStringLiteral("PartiallyReady"));
        RealCoreClient client(partialPath, nullptr);
        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(1), 1000);
        QCOMPARE(client.aggregateState(), QStringLiteral("PartiallyReady"));
        QCOMPARE(client.aggregateStateText(), QStringLiteral("Partially protected"));
        QCOMPARE(client.aggregateActionText(), QStringLiteral("DISCONNECT"));
        client.toggleAll();
        QTRY_COMPARE_WITH_TIMEOUT(fixture.lastMethod(), QStringLiteral("DisconnectAll"), 1000);

        const QString recoveringPath = QDir::temp().filePath(QStringLiteral("kikimora-core-recovering-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture recoveringFixture(recoveringPath);
        recoveringFixture.setAggregateState(QStringLiteral("Recovering"));
        RealCoreClient recoveringClient(recoveringPath, nullptr);
        QTRY_COMPARE_WITH_TIMEOUT(recoveringClient.revision(), static_cast<qulonglong>(1), 1000);
        QCOMPARE(recoveringClient.aggregateStateText(), QStringLiteral("Partially protected · recovering"));
        QCOMPARE(recoveringClient.aggregateActionText(), QStringLiteral("DISCONNECT"));
        recoveringClient.toggleAll();
        QTRY_COMPARE_WITH_TIMEOUT(recoveringFixture.lastMethod(), QStringLiteral("DisconnectAll"), 1000);

        const QString stoppedPath = QDir::temp().filePath(QStringLiteral("kikimora-core-stopped-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture stoppedFixture(stoppedPath);
        stoppedFixture.setAggregateState(QStringLiteral("Stopped"));
        RealCoreClient stoppedClient(stoppedPath, nullptr);
        QTRY_COMPARE_WITH_TIMEOUT(stoppedClient.revision(), static_cast<qulonglong>(1), 1000);
        QCOMPARE(stoppedClient.aggregateStateText(), QStringLiteral("Not connected"));
        QCOMPARE(stoppedClient.aggregateActionText(), QStringLiteral("CONNECT"));
    }

    // The default-route row reads its rule from GetRoutingLists: the snapshot does
    // not carry bypass_mode, and a SetBypassMode reply carries a message only. A
    // client that skipped the request would keep claiming "direct" forever.
    void readsTheConfirmedBypassRuleFromTheCore()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-bypass-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setBypassMode(QStringLiteral("office"));
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.bypassMode(), QStringLiteral("office"), 1000);
        QVERIFY(fixture.routingListRequests() >= 1);

        fixture.setBypassMode(QStringLiteral("direct"));
        client.setBypassMode(QStringLiteral("direct"));
        QTRY_VERIFY_WITH_TIMEOUT(fixture.routingListRequests() >= 2, 1000);
        QTRY_COMPARE_WITH_TIMEOUT(client.bypassMode(), QStringLiteral("direct"), 1000);
    }
};

QTEST_MAIN(RealCoreClientTest)
#include "RealCoreClientTest.moc"
