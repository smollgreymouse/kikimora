#include "core/RealCoreClient.h"

#include <QFile>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QDir>
#include <QLocalServer>
#include <QSet>
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
    void setStaleResync(bool enabled) { m_staleResync = enabled; }
    int routingListRequests() const { return m_routingListRequests; }
    QString lastMethod() const { return m_lastMethod; }
    // lastMethod is overwritten by follow-up requests (a capture toggle is
    // always chased by a diagnostics refresh); this records every method.
    bool sawMethod(const QString &method) const { return m_methods.contains(method); }
    // --- U1 test controls: managed authority truth, handshake window, role extras ---
    void setStartupReady(bool ready) { m_startupReady = ready; }
    void setManagedAuthority(bool managed, bool ready)
    {
        m_managedMode = managed;
        m_routeAuthorityReady = ready;
    }
    void setModeTransition(const QString &phase) { m_modeTransition = phase; }
    void setHandshakeRange(int minVersion, int maxVersion)
    {
        m_minVersion = minVersion;
        m_maxVersion = maxVersion;
    }
    void setRoleFamilies(const QJsonObject &families) { m_roleFamilies = families; }
    void setSelectedRoutes(const QJsonObject &stats) { m_selectedRoutes = stats; }
    void setRoleLeshyIpv6(bool enabled) { m_roleLeshyIpv6 = enabled; }
    void setActivationFingerprint(const QString &fingerprint) { m_activationFingerprint = fingerprint; }
    // Drop the client connection from the fixture side (core process death).
    void disconnectClient()
    {
#ifdef Q_OS_WIN
        if (m_client != INVALID_SOCKET) {
            delete m_clientNotifier;
            m_clientNotifier = nullptr;
            ::closesocket(m_client);
            m_client = INVALID_SOCKET;
            // Re-arm the accept notifier: the reconnect test dials a second
            // time, and an accept loop left disabled never admits it.
            m_acceptNotifier->setEnabled(true);
        }
#else
        if (m_socket) m_socket->disconnectFromServer();
#endif
    }

    // A streamed snapshot with no request behind it: what the core pushes to a subscriber.
    void pushSnapshot(const QString &profile, int revision)
    {
        QJsonObject response{{QStringLiteral("version"), 1},
                             {QStringLiteral("id"), QStringLiteral("subscribe")},
                             {QStringLiteral("ok"), true}};
        response.insert(QStringLiteral("snapshot"), buildSnapshot(profile, revision));
        sendFrame(QJsonDocument(response).toJson(QJsonDocument::Compact));
    }

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
    // One snapshot shape for every reply: the U1 safety fields are always
    // present, so a client that stops parsing them cannot pass these tests.
    QJsonObject buildSnapshot(const QString &profile, int revision) const
    {
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
                         {QStringLiteral("route_ready"), true},
                         {QStringLiteral("leshy_ipv6"), m_roleLeshyIpv6},
                         {QStringLiteral("available_actions"), QJsonArray{QStringLiteral("disconnect"), QStringLiteral("retry")}}};
        if (!m_roleFamilies.isEmpty()) role.insert(QStringLiteral("families"), m_roleFamilies);
        if (!m_selectedRoutes.isEmpty()) role.insert(QStringLiteral("selected_routes"), m_selectedRoutes);
        QJsonObject snapshot{
            {QStringLiteral("schema"), 1},
            {QStringLiteral("revision"), revision},
            {QStringLiteral("core_state"), QStringLiteral("Ready")},
            {QStringLiteral("aggregate_state"), m_aggregateState.isEmpty() ? QStringLiteral("Ready") : m_aggregateState},
            {QStringLiteral("active_profile"), profile},
            {QStringLiteral("profiles"), QJsonArray{QStringLiteral("default"), QStringLiteral("work"), QStringLiteral("home")}},
            {QStringLiteral("underlay_summary"), QStringLiteral("Wi-Fi")},
            {QStringLiteral("leshy_supported"), false},
            {QStringLiteral("startup_ready"), m_startupReady},
            {QStringLiteral("managed_mode"), m_managedMode},
            {QStringLiteral("mode_transition"), m_modeTransition},
            {QStringLiteral("route_authority_ready"), m_routeAuthorityReady},
            {QStringLiteral("roles"), QJsonArray{role}}};
        if (!m_routeAuthorityError.isEmpty())
            snapshot.insert(QStringLiteral("route_authority_error"), m_routeAuthorityError);
        if (!m_activationFingerprint.isEmpty())
            snapshot.insert(QStringLiteral("activation_id_fingerprint"), m_activationFingerprint);
        return snapshot;
    }

    void sendFrame(const QByteArray &payload)
    {
        QByteArray frame(4, Qt::Uninitialized);
        const quint32 size = static_cast<quint32>(payload.size());
        frame[0] = static_cast<char>((size >> 24) & 0xff);
        frame[1] = static_cast<char>((size >> 16) & 0xff);
        frame[2] = static_cast<char>((size >> 8) & 0xff);
        frame[3] = static_cast<char>(size & 0xff);
        sendToClient(frame + payload);
    }

#ifdef Q_OS_WIN
    void acceptClient()
    {
        m_client = ::accept(m_listen, nullptr, nullptr);
        if (m_client == INVALID_SOCKET) return;
        m_acceptNotifier->setEnabled(false);
        u_long nonBlocking = 1;
        ioctlsocket(m_client, FIONBIO, &nonBlocking);
        delete m_clientNotifier;
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
        m_methods.insert(method);
        if (method == QStringLiteral("SetActiveProfile")) {
            m_lastSetActiveProfile = request.value(QStringLiteral("profile")).toString();
        }
        QJsonObject response{{QStringLiteral("version"), 1},
                             {QStringLiteral("id"), request.value(QStringLiteral("id"))},
                             {QStringLiteral("ok"), true}};
        if (method == QStringLiteral("Handshake")) {
            response.insert(QStringLiteral("capabilities"), QJsonArray{QStringLiteral("Subscribe")});
            response.insert(QStringLiteral("min_version"), m_minVersion);
            response.insert(QStringLiteral("max_version"), m_maxVersion);
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
            const bool isResync = request.value(QStringLiteral("id")).toString() == QStringLiteral("resync");
            const int revision = m_emitRevisionGap &&
                                         (method == QStringLiteral("Subscribe") || isResync) &&
                                         !(isResync && m_staleResync)
                                     ? 3
                                     : 1;
            response.insert(QStringLiteral("snapshot"), buildSnapshot(QStringLiteral("default"), revision));
        }
        sendFrame(QJsonDocument(response).toJson(QJsonDocument::Compact));
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
    QSet<QString> m_methods;
    QString m_bypassMode = QStringLiteral("direct");
    int m_routingListRequests = 0;
    bool m_staleResync = false;
    // U1 controls; defaults mirror a healthy local-mode core.
    bool m_startupReady = true;
    bool m_managedMode = false;
    bool m_routeAuthorityReady = false;
    QString m_modeTransition = QStringLiteral("none");
    QString m_routeAuthorityError;
    QString m_activationFingerprint;
    int m_minVersion = 1;
    int m_maxVersion = 3;
    QJsonObject m_roleFamilies;
    QJsonObject m_selectedRoutes;
    bool m_roleLeshyIpv6 = true;
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

    // A resync reply that is not newer than the applied state still ends the wait: a gap
    // used to leave m_resyncPending set forever, freezing every later snapshot. This is the
    // regression that made the window stop following the core after rapid profile switches.
    void keepsUpdatingAfterAStaleResyncReply()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-stale-resync-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setEmitRevisionGap(true);
        fixture.setStaleResync(true);
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(1), 1000);
        QCOMPARE(client.activeProfile(), QStringLiteral("default"));

        fixture.pushSnapshot(QStringLiteral("work"), 2);
        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(2), 1000);
        QCOMPARE(client.activeProfile(), QStringLiteral("work"));
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

    // --- U1: current core truth (roadmap §8) ---

    void mapsManagedAuthorityFields()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-authority-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setManagedAuthority(true, false);
        fixture.setModeTransition(QStringLiteral("config_written"));
        fixture.setRoleFamilies(QJsonObject{
            {QStringLiteral("ipv4"), QJsonObject{{QStringLiteral("configured"), true}, {QStringLiteral("ready"), true}}},
            {QStringLiteral("ipv6"), QJsonObject{{QStringLiteral("configured"), true}, {QStringLiteral("ready"), false}, {QStringLiteral("reason"), QStringLiteral("no v6 route")}}}});
        fixture.setSelectedRoutes(QJsonObject{
            {QStringLiteral("registered"), 3}, {QStringLiteral("active"), 2},
            {QStringLiteral("conflicts"), 1}, {QStringLiteral("denials"), 4}});
        fixture.setActivationFingerprint(QStringLiteral("9f2a"));
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(1), 1000);
        QCOMPARE(client.managedMode(), true);
        QCOMPARE(client.routeAuthorityReady(), false);
        QCOMPARE(client.modeTransition(), QStringLiteral("config_written"));
        QCOMPARE(client.activationIdFingerprint(), QStringLiteral("9f2a"));
        QCOMPARE(client.startupReady(), true);
        QCOMPARE(client.modeGeneration(), static_cast<qulonglong>(0));
        // An in-flight mode transition gates everything; authority loss on a
        // settled channel gates mutation but leaves disconnect available.
        QCOMPARE(client.safeToMutate(), false);
        QCOMPARE(client.safeToDisconnect(), false);
        fixture.setModeTransition(QStringLiteral("none"));
        fixture.pushSnapshot(QStringLiteral("default"), 2);
        QTRY_COMPARE_WITH_TIMEOUT(client.modeTransition(), QStringLiteral("none"), 1000);
        QCOMPARE(client.safeToMutate(), false);
        QCOMPARE(client.safeToDisconnect(), true);
        // The role carries the family truth and the counters unchanged.
        const RoleSnapshot role = client.roles()->roleAt(0);
        QCOMPARE(role.ipv4Configured, true);
        QCOMPARE(role.ipv4Ready, true);
        QCOMPARE(role.ipv6Configured, true);
        QCOMPARE(role.ipv6Ready, false);
        QCOMPARE(role.ipv6Reason, QStringLiteral("no v6 route"));
        QCOMPARE(role.selectedRoutes.value(QStringLiteral("registered")).toInt(), 3);
        QCOMPARE(role.selectedRoutes.value(QStringLiteral("conflicts")).toInt(), 1);
        QCOMPARE(role.selectedRoutes.value(QStringLiteral("denials")).toInt(), 4);
    }

    void mapsIndependentFamilyReadiness()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-families-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setRoleFamilies(QJsonObject{
            {QStringLiteral("ipv4"), QJsonObject{{QStringLiteral("configured"), true}, {QStringLiteral("ready"), true}}},
            {QStringLiteral("ipv6"), QJsonObject{{QStringLiteral("configured"), false}, {QStringLiteral("ready"), false}}}});
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.roles()->rowCount(), 1, 1000);
        const RoleSnapshot role = client.roles()->roleAt(0);
        // route_ready may be true while IPv6 is unconfigured: the families are
        // independent and never derived from the legacy bit.
        QCOMPARE(role.routeReady, true);
        QCOMPARE(role.ipv4Ready, true);
        QCOMPARE(role.ipv6Configured, false);
        QCOMPARE(role.ipv6Ready, false);
        const QVariantMap map = client.roles()->get(0);
        QCOMPARE(map.value(QStringLiteral("ipv4Ready")).toBool(), true);
        QCOMPARE(map.value(QStringLiteral("ipv6Configured")).toBool(), false);
        QCOMPARE(map.value(QStringLiteral("ipv6Ready")).toBool(), false);
    }

    void mapsSelectedRouteStats()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-selroutes-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setSelectedRoutes(QJsonObject{
            {QStringLiteral("registered"), 3}, {QStringLiteral("active"), 3},
            {QStringLiteral("parked"), 1}, {QStringLiteral("retiring"), 0},
            {QStringLiteral("drift_repairs"), 2}, {QStringLiteral("park_pending"), 0},
            {QStringLiteral("park_applying"), 1}, {QStringLiteral("park_active"), 1},
            {QStringLiteral("park_retiring"), 0}, {QStringLiteral("kernel_mutations"), 9},
            {QStringLiteral("denials"), 4}, {QStringLiteral("conflicts"), 1}});
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.roles()->rowCount(), 1, 1000);
        const QVariantMap stats = client.roles()->roleAt(0).selectedRoutes;
        QCOMPARE(stats.value(QStringLiteral("registered")).toInt(), 3);
        QCOMPARE(stats.value(QStringLiteral("drift_repairs")).toInt(), 2);
        QCOMPARE(stats.value(QStringLiteral("park_active")).toInt(), 1);
        QCOMPARE(stats.value(QStringLiteral("kernel_mutations")).toInt(), 9);
        QCOMPARE(stats.value(QStringLiteral("denials")).toInt(), 4);
        QCOMPARE(stats.value(QStringLiteral("conflicts")).toInt(), 1);
        // And get() must carry the same map for the diagnostics page.
        const QVariantMap fromGet = client.roles()->get(0).value(QStringLiteral("selectedRoutes")).toMap();
        QCOMPARE(fromGet.value(QStringLiteral("denials")).toInt(), 4);
    }

    void acceptsApiV3HandshakeAndCapabilities()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-v3-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(1), 1000);
        QCOMPARE(client.apiCompatible(), true);
        QCOMPARE(client.coreState(), QStringLiteral("Ready"));
        QCOMPARE(client.commandChannelReady(), true);
        QVERIFY(client.coreCapabilities().contains(QStringLiteral("Subscribe")));
    }

    void rejectsHandshakeWhenApi3IsOutsideServerRange()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-oldcore-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setHandshakeRange(1, 2);
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.coreState(), QStringLiteral("Incompatible"), 1000);
        QCOMPARE(client.apiCompatible(), false);
        QCOMPARE(client.commandChannelReady(), false);
        QCOMPARE(client.safeToMutate(), false);
        QVERIFY(client.lastCommandError().contains(QStringLiteral("does not support client API 3")));
        // An incompatible core is a terminal state for this connection: the
        // client must not loop identical negotiations forever. Only the
        // handshake was exchanged, no snapshot request followed.
        const int requestsAfterHandshake = fixture.requestCount();
        QTest::qWait(700);
        QCOMPARE(fixture.requestCount(), requestsAfterHandshake);
    }

    void commandChannelWaitsForAuthoritativeSnapshot()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-startup-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setStartupReady(false);
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(1), 1000);
        QCOMPARE(client.apiCompatible(), true);
        QCOMPARE(client.startupReady(), false);
        QCOMPARE(client.commandChannelReady(), false);
        // Mutations stay blocked until the authoritative snapshot says ready.
        const int before = fixture.requestCount();
        client.connectAll();
        QCOMPARE(fixture.requestCount(), before);
        QVERIFY(client.lastCommandError().contains(QStringLiteral("blocked")));

        fixture.setStartupReady(true);
        fixture.pushSnapshot(QStringLiteral("default"), 2);
        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(2), 1000);
        QCOMPARE(client.commandChannelReady(), true);
        client.connectAll();
        QTRY_COMPARE_WITH_TIMEOUT(fixture.lastMethod(), QStringLiteral("ConnectAll"), 1000);
    }

    void disconnectClearsCommandChannelReadiness()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-drop-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setManagedAuthority(true, true);
        RealCoreClient client(path, nullptr);

        QTRY_VERIFY_WITH_TIMEOUT(client.commandChannelReady(), 1000);
        // While the authority holds, mutation is available and DisconnectAll
        // would be sent.
        QCOMPARE(client.safeToMutate(), true);
        fixture.disconnectClient();
        QTRY_COMPARE_WITH_TIMEOUT(client.coreState(), QStringLiteral("Unavailable"), 1000);
        QCOMPARE(client.commandChannelReady(), false);
        QCOMPARE(client.safeToMutate(), false);
        QCOMPARE(client.safeToDisconnect(), false);
        QCOMPARE(client.diagnosticsAvailable(), false);
        QCOMPARE(client.diagnosticsMutable(), false);
        QCOMPARE(client.startupReady(), false);
        QCOMPARE(client.apiCompatible(), false);
    }

    void reconnectRequiresNewHandshakeAndSnapshot()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-reconnect-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        RealCoreClient client(path, nullptr);

        QTRY_VERIFY_WITH_TIMEOUT(client.commandChannelReady(), 1000);
        const int before = fixture.requestCount();
        fixture.disconnectClient();
        QTRY_COMPARE_WITH_TIMEOUT(client.coreState(), QStringLiteral("Unavailable"), 1000);
        QCOMPARE(client.commandChannelReady(), false);
        // The client redials (500 ms backoff) and must negotiate a fresh
        // handshake plus authoritative snapshot before commands open again.
        QTRY_VERIFY_WITH_TIMEOUT(fixture.requestCount() > before, 5000);
        QTRY_VERIFY_WITH_TIMEOUT(client.commandChannelReady(), 3000);
        QCOMPARE(client.coreState(), QStringLiteral("Ready"));
    }

    void commandErrorClearsOnNextSuccessfulCommand()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-clear-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        RealCoreClient client(path, nullptr);
        QTRY_COMPARE_WITH_TIMEOUT(client.roles()->rowCount(), 1, 1000);

        client.retryRole(0);
        QTRY_COMPARE_WITH_TIMEOUT(client.lastCommandError(), QStringLiteral("role validation failed"), 1000);
        // A later successful command clears the stale error; streamed
        // snapshots in between must not (the fixture pushes none here anyway).
        client.refreshRoutingLists();
        QTRY_VERIFY_WITH_TIMEOUT(client.lastCommandError().isEmpty(), 1000);
    }

    void authorityFieldsDoNotSurviveDisconnectAsActionableTruth()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-staleauth-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setManagedAuthority(true, true);
        RealCoreClient client(path, nullptr);

        QTRY_VERIFY_WITH_TIMEOUT(client.routeAuthorityReady(), 1000);
        fixture.disconnectClient();
        QTRY_COMPARE_WITH_TIMEOUT(client.coreState(), QStringLiteral("Unavailable"), 1000);
        // The old connection's authority must not read as actionable truth.
        QCOMPARE(client.routeAuthorityReady(), false);
        QCOMPARE(client.managedMode(), false);
        QCOMPARE(client.safeToMutate(), false);
    }

    void safePolicyBlocksConnectDuringAuthorityLoss()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-lossconn-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setManagedAuthority(true, false);
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(1), 1000);
        QCOMPARE(client.safeToMutate(), false);
        QCOMPARE(client.safeToDisconnect(), true);
        const int before = fixture.requestCount();
        client.connectAll();
        QCOMPARE(fixture.requestCount(), before);
        QVERIFY(client.lastCommandError().contains(QStringLiteral("blocked")));
    }

    void safePolicyAllowsDisconnectDuringAuthorityLoss()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-lossdisc-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setManagedAuthority(true, false);
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(1), 1000);
        client.disconnectAll();
        QTRY_COMPARE_WITH_TIMEOUT(fixture.lastMethod(), QStringLiteral("DisconnectAll"), 1000);
        client.roleActionByName(0, QStringLiteral("disconnect"));
        QTRY_COMPARE_WITH_TIMEOUT(fixture.lastMethod(), QStringLiteral("DisconnectRole"), 1000);
    }

    void safePolicyBlocksAllCommandsBeforeHandshakeSnapshot()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-presnap-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setStartupReady(false);
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(1), 1000);
        const int before = fixture.requestCount();
        client.connectAll();
        client.disconnectAll();
        client.setRoleIpv6(0, false);
        QCOMPARE(fixture.requestCount(), before);
        QVERIFY(client.lastCommandError().contains(QStringLiteral("blocked")));
    }

    void programmaticBlockedMutationSendsNoFrame()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-noframe-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setManagedAuthority(true, false);
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(1), 1000);
        const int before = fixture.requestCount();
        const QString lastMethod = fixture.lastMethod();
        client.createTunnel({{QStringLiteral("name"), QStringLiteral("blocked")}, {QStringLiteral("protocol"), QStringLiteral("AmneziaWG")}});
        client.removeTunnel(QStringLiteral("office"));
        client.setBypassMode(QStringLiteral("office"));
        client.saveRoutingList(QStringLiteral("office"), QStringLiteral("example.com"));
        client.rediscoverEndpoints(0);
        QCOMPARE(fixture.requestCount(), before);
        QCOMPARE(fixture.lastMethod(), lastMethod);
        QVERIFY(client.lastCommandError().contains(QStringLiteral("blocked")));
    }

    void blockedProfileSwitchDoesNotChangeOptimisticProfile()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-blockprofile-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setManagedAuthority(true, false);
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(1), 1000);
        client.setActiveProfile(QStringLiteral("work"));
        QCOMPARE(client.activeProfile(), QStringLiteral("default"));
        QVERIFY(client.lastCommandError().contains(QStringLiteral("blocked")));
    }

    void blockedMembershipEditDoesNotChangeOptimisticMembership()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-blockmember-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setManagedAuthority(true, false);
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(1), 1000);
        const QStringList before = client.profilesForRole(QStringLiteral("Office"));
        client.setTunnelProfiles(QStringLiteral("Office"), {QStringLiteral("work")});
        QCOMPARE(client.profilesForRole(QStringLiteral("Office")), before);
        QVERIFY(client.lastCommandError().contains(QStringLiteral("blocked")));
    }

    void diagnosticsReadAllowedDuringAuthorityLoss()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-diagread-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setManagedAuthority(true, false);
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(1), 1000);
        client.refreshDiagnostics();
        QTRY_COMPARE_WITH_TIMEOUT(fixture.lastMethod(), QStringLiteral("GetDiagnosticsSnapshot"), 1000);
        client.refreshRoutingLists();
        QTRY_VERIFY_WITH_TIMEOUT(fixture.routingListRequests() >= 2, 1000);
    }

    void diagnosticsCaptureAllowedDuringAuthorityLossWhenNoTransition()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-diagcap-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setManagedAuthority(true, false);
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(1), 1000);
        client.setDiagnosticsCapture(true);
        // setDiagnosticsCapture also chases a diagnostics refresh, so the
        // last request is not the capture itself: both frames must be seen.
        QTRY_VERIFY_WITH_TIMEOUT(fixture.sawMethod(QStringLiteral("SetDiagnosticsCapture")), 1000);
    }

    void modeTransitionBlocksDiagnosticsCaptureButNotRead()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-transition-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        fixture.setModeTransition(QStringLiteral("prepared"));
        RealCoreClient client(path, nullptr);

        QTRY_COMPARE_WITH_TIMEOUT(client.revision(), static_cast<qulonglong>(1), 1000);
        QCOMPARE(client.safeToMutate(), false);
        QCOMPARE(client.safeToDisconnect(), false);
        QCOMPARE(client.diagnosticsAvailable(), true);
        QCOMPARE(client.diagnosticsMutable(), false);
        // Let the initial handshake burst settle before counting frames.
        QTRY_VERIFY_WITH_TIMEOUT(fixture.sawMethod(QStringLiteral("GetRoutingLists")), 1000);
        const int before = fixture.requestCount();
        client.setDiagnosticsCapture(true);
        QCOMPARE(fixture.requestCount(), before);
        client.refreshDiagnostics();
        QTRY_COMPARE_WITH_TIMEOUT(fixture.lastMethod(), QStringLiteral("GetDiagnosticsSnapshot"), 1000);
        QCOMPARE(fixture.requestCount(), before + 1);
    }

    void disconnectPolicyClearsOnSocketLoss()
    {
        const QString path = QDir::temp().filePath(QStringLiteral("kikimora-core-discpolicy-%1.sock").arg(QCoreApplication::applicationPid()));
        IpcFixture fixture(path);
        RealCoreClient client(path, nullptr);

        QTRY_VERIFY_WITH_TIMEOUT(client.safeToDisconnect(), 1000);
        fixture.disconnectClient();
        QTRY_COMPARE_WITH_TIMEOUT(client.coreState(), QStringLiteral("Unavailable"), 1000);
        QCOMPARE(client.safeToDisconnect(), false);
        const int before = fixture.requestCount();
        client.disconnectAll();
        QCOMPARE(fixture.requestCount(), before);
        QVERIFY(client.lastCommandError().contains(QStringLiteral("blocked")));
    }
};

QTEST_MAIN(RealCoreClientTest)
#include "RealCoreClientTest.moc"
