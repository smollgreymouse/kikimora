#include "core/RealCoreClient.h"
#include "support/LiveCoreFixture.h"

#include <QElapsedTimer>
#include <QTest>

// Layer C (U5): real kikimora-core process + real IPC + real temp filesystem
// through the hermetic LiveCoreFixture — no QML, protocol/round-trip proof
// only. The suite owns its core, so a missing binary is a harness failure,
// never a skip (docs/roadmap.md §12).

namespace {
// Ready readiness reads the display state: a schema-2 core reports the raw
// transport state ("online"), the display mapping stays "Ready" either way.
bool allRoles(RealCoreClient &client, const QString &displayState)
{
    if (client.roles()->rowCount() != 2) return false;
    for (int row = 0; row < client.roles()->rowCount(); ++row) {
        const RoleSnapshot role = client.roles()->roleAt(row);
        if (role.stateText != displayState) return false;
        if (displayState == QStringLiteral("Ready") && !role.sessionConnected) return false;
    }
    return true;
}
} // namespace

class RealCoreEndToEndTest final : public QObject
{
    Q_OBJECT

private slots:
    void handshakeSnapshotAndLifecycle()
    {
        QString coreBinary, toadBinary, error;
        QVERIFY2(UiTest::LiveCoreFixture::resolveBinaries(&coreBinary, &toadBinary, &error),
                 qPrintable(error));
        UiTest::LiveCoreFixture fixture(coreBinary, toadBinary);
        QVERIFY2(fixture.start(&error), qPrintable(error));

        RealCoreClient client(fixture.socketPath(), nullptr);
        // Handshake v3 + authoritative Snapshot + Subscribe happened once the
        // revision advances and the deterministic roles are mapped.
        QTRY_VERIFY_WITH_TIMEOUT(client.revision() > 0, 5000);
        QCOMPARE(client.apiCompatible(), true);
        QVERIFY(client.coreCapabilities().contains(QStringLiteral("Subscribe")));
        QCOMPARE(client.commandChannelReady(), true);
        QTRY_VERIFY_WITH_TIMEOUT(client.roles()->rowCount() == 2, 3000);
        QCOMPARE(client.profiles().contains(QStringLiteral("default")), true);
        QCOMPARE(client.profiles().contains(QStringLiteral("work")), true);
        QCOMPARE(client.profiles().contains(QStringLiteral("home")), true);

        const qulonglong initialRevision = client.revision();

        // Role actions drive the real core through the fake Toad transports.
        client.roleActionByName(0, QStringLiteral("connect"));
        QTRY_VERIFY_WITH_TIMEOUT(client.roles()->roleAt(0).stateText == QStringLiteral("Ready"), 15000);
        client.connectAll();
        QElapsedTimer lifecycleTimer;
        lifecycleTimer.start();
        while (!allRoles(client, QStringLiteral("Ready")) && lifecycleTimer.elapsed() < 20000)
            QTest::qWait(50);
        if (!allRoles(client, QStringLiteral("Ready"))) {
            QString states;
            for (int row = 0; row < client.roles()->rowCount(); ++row) {
                const RoleSnapshot role = client.roles()->roleAt(row);
                states += QStringLiteral("%1=%2/%3 connected=%4 error=%5; ")
                              .arg(role.id, role.state, role.stateText)
                              .arg(role.sessionConnected)
                              .arg(role.lastError);
            }
            QFAIL(qPrintable(QStringLiteral("roles did not become ready: %1\ncore stderr: %2")
                                 .arg(states, fixture.output())));
        }
        QVERIFY(client.revision() > initialRevision);

        client.disconnectAll();
        QTRY_VERIFY_WITH_TIMEOUT(allRoles(client, QStringLiteral("Stopped")), 10000);

        // Profile switch round-trips through the real control transport.
        client.setActiveProfile(QStringLiteral("work"));
        QTRY_COMPARE_WITH_TIMEOUT(client.activeProfile(), QStringLiteral("work"), 5000);
        client.setActiveProfile(QStringLiteral("default"));
        QTRY_COMPARE_WITH_TIMEOUT(client.activeProfile(), QStringLiteral("default"), 5000);
    }

    void coreRestartDisablesThenRestoresCommands()
    {
        QString coreBinary, toadBinary, error;
        QVERIFY2(UiTest::LiveCoreFixture::resolveBinaries(&coreBinary, &toadBinary, &error),
                 qPrintable(error));
        UiTest::LiveCoreFixture fixture(coreBinary, toadBinary);
        QVERIFY2(fixture.start(&error), qPrintable(error));

        RealCoreClient client(fixture.socketPath(), nullptr);
        QTRY_VERIFY_WITH_TIMEOUT(client.commandChannelReady(), 5000);

        // Killing the core closes the channel: no stale actionable truth may
        // survive the socket.
        QVERIFY2(fixture.restart(&error), qPrintable(error));
        QTRY_COMPARE_WITH_TIMEOUT(client.coreState(), QStringLiteral("Unavailable"), 5000);
        QCOMPARE(client.commandChannelReady(), false);
        QCOMPARE(client.safeToMutate(), false);
        QCOMPARE(client.safeToDisconnect(), false);
        QCOMPARE(client.startupReady(), false);

        // The client redials and re-negotiates on its own: commands open only
        // after the new connection's authoritative snapshot.
        QTRY_VERIFY_WITH_TIMEOUT(client.commandChannelReady(), 10000);
        QCOMPARE(client.coreState(), QStringLiteral("Ready"));
        QVERIFY(client.revision() > 0);
        client.connectAll();
        QTRY_VERIFY_WITH_TIMEOUT(client.aggregateState() == QStringLiteral("Ready"), 15000);
    }
};

QTEST_MAIN(RealCoreEndToEndTest)
#include "RealCoreEndToEndTest.moc"
