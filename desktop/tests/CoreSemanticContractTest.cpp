#include "core/CoreBackend.h"
#include "core/FakeCoreBackend.h"
#include "core/RealCoreClient.h"
#include "support/LiveCoreFixture.h"

#include <QElapsedTimer>
#include <QSignalSpy>
#include <QTest>

// 09G-W0.3: one shared FakeCore/RealCore behavior contract. The SAME row
// table runs against both backends through a small test-only adapter; the
// expected contract is the TARGET product contract (09g-w3 plan), so the
// current RealCore create/remove rows fail until W3 implements the live
// role lifecycle. That divergence is the red gate W0 leaves standing: it
// may not be skipped, weakened or silenced — W3 turns it green.
//
// Locate nothing by list row: every row resolves roles by their stable id.

namespace {

struct SemanticBackend
{
    QString lane;                    // "FakeCore" | "RealCore"
    CoreBackend *core = nullptr;     // not owned
    QString loadedStoppedRole;       // the removeTunnel argument for the loaded role
    QString loadedStoppedRoleId;     // the id the removal is verified against
    QString loadedRoleProfile;       // a profile the loaded role belongs to
    std::function<void()> settle;    // converge to a fresh authoritative view
    // The canonical create/import path: a role named `name` in `profile`.
    std::function<void(const QString &name, const QString &profile)> seedImport;
};

QString findRowById(CoreBackend *core, const QString &id)
{
    RoleListModel *roles = core->roles();
    for (int row = 0; row < roles->rowCount(); ++row) {
        if (roles->roleAt(row).id == id) return QString::number(row);
    }
    return QString();
}

// The canonical import payload: JSON with name+protocol (TunnelImport's
// minimum) and the vless-reality keys the create path carries. Both lanes
// receive the exact same spec.
QString importPayload(const QString &name)
{
    return QStringLiteral(R"({"name":"%1","protocol":"vless-reality",)"
                          R"("endpoint":"contract.example:443",)"
                          R"("uuid":"00000000-0000-0000-0000-000000000000",)"
                          R"("server_name":"contract.example",)"
                          R"("public_key":"cONTRACTkEyPlaceholder0000000000000000=",)"
                          R"("short_id":"00", "flow":"xtls-rprx-vision"})")
        .arg(name);
}

// Runs the full row table; returns a list of failing rows with reasons.
QStringList runContractRows(const SemanticBackend &backend)
{
    CoreBackend *core = backend.core;
    QStringList failures;
    const auto record = [&failures](const QString &row, bool ok, const QString &detail) {
        if (!ok) failures.append(QStringLiteral("%1: %2").arg(row, detail));
    };

    // --- row 1: create profile becomes visible immediately ---
    const QString profile = QStringLiteral("contract-pro");
    {
        core->createProfile(profile, {});
        backend.settle();
        record(QStringLiteral("create-profile-visible-immediately"),
               core->profiles().contains(profile),
               QStringLiteral("profiles() after createProfile+%1 does not contain %2 (lane %3)")
                   .arg(backend.lane, profile, backend.lane));
    }

    // --- rows 2-5: create/import role becomes visible immediately without a
    // core restart; desired=false; membership lives in the requested profile
    // and mutates live ---
    const QString role = QStringLiteral("contract-role");
    {
        QSignalSpy finished(core, SIGNAL(profileCreationFinished(QString, bool, QString)));
        backend.seedImport(role, profile);
        backend.settle();
        const QString row = findRowById(core, role);
        record(QStringLiteral("import-role-visible-immediately"), !row.isNull(),
               QStringLiteral("no role with id %1 after the import + settle (lane %2)")
                   .arg(role, backend.lane));
        if (!row.isNull()) {
            const RoleSnapshot snapshot = core->roles()->roleAt(row.toInt());
            record(QStringLiteral("created-role-desired-false"), !snapshot.desiredEnabled,
                   QStringLiteral("created role %1 reports desiredEnabled=%2 (lane %3)")
                       .arg(role)
                       .arg(snapshot.desiredEnabled)
                       .arg(backend.lane));
            record(QStringLiteral("created-role-in-requested-profile"),
                   core->profilesForRole(role).contains(profile),
                   QStringLiteral("profilesForRole(%1)=%2 lacks %3 (lane %4)")
                       .arg(role, core->profilesForRole(role).join(u','), profile, backend.lane));

            // --- membership mutation is live ---
            const QString other = QStringLiteral("default");
            core->setTunnelProfiles(role, {other});
            backend.settle();
            record(QStringLiteral("membership-mutation-is-live"),
                   core->profilesForRole(role).contains(other)
                       && !core->profilesForRole(role).contains(profile),
                   QStringLiteral("after setTunnelProfiles(%1,[%2]) the membership is %3 (lane %4)")
                       .arg(role, other, core->profilesForRole(role).join(u','), backend.lane));

            // --- row 8: role identity is stable by id, not list row ---
            core->setBypassMode(QStringLiteral("contract-bypass"));
            backend.settle();
            const QString rowAfter = findRowById(core, role);
            const bool identityStable = !rowAfter.isNull()
                && core->roles()->roleAt(rowAfter.toInt()).id == role
                && core->roles()->roleAt(rowAfter.toInt()).protocol == QStringLiteral("vless-reality");
            record(QStringLiteral("role-identity-stable-by-id"), identityStable,
                   QStringLiteral("role %1 is not resolvable by id with unchanged identity after a "
                                  "revision bump (lane %2)")
                       .arg(role, backend.lane));
        }
    }

    // --- row: remove loaded stopped role removes it live ---
    {
        core->removeTunnel(backend.loadedStoppedRole);
        backend.settle();
        record(QStringLiteral("remove-loaded-stopped-role-live"),
               findRowById(core, backend.loadedStoppedRoleId).isNull(),
               QStringLiteral("loaded stopped role %1 survived removeTunnel + settle (lane %2)")
                   .arg(backend.loadedStoppedRoleId, backend.lane));
    }

    // --- row: command rejection produces a completion result ---
    {
        QSignalSpy finished(core, SIGNAL(profileCreationFinished(QString, bool, QString)));
        core->createProfile(QString(), {});
        backend.settle();
        const bool completion = finished.count() > 0 && finished.constFirst().at(1).toBool() == false;
        const bool reported = !core->lastCommandError().isEmpty();
        record(QStringLiteral("command-rejection-produces-completion"), completion && reported,
               QStringLiteral("empty createProfile: completion signal=%1 lastCommandError=%2 (lane %3)")
                   .arg(finished.count())
                   .arg(core->lastCommandError(), backend.lane));
    }

    return failures;
}

} // namespace

class CoreSemanticContractTest final : public QObject
{
    Q_OBJECT

private slots:
    // The fake must satisfy the target contract outright: it has no restart
    // boundary to hide behind, so a red fake row is a fake defect.
    void fakeCoreMatchesTargetContract()
    {
        FakeCoreBackend fake;
        SemanticBackend backend;
        backend.lane = QStringLiteral("FakeCore");
        backend.core = &fake;
        // The fake's role ids ARE its labels today; removeTunnel matches the
        // id ("Primary"), verified by id ("primary" below) — W2's stable-id
        // work owns that distinction.
        backend.loadedStoppedRole = QStringLiteral("Primary");
        backend.loadedStoppedRoleId = QStringLiteral("primary");
        backend.loadedRoleProfile = QStringLiteral("default");
        backend.settle = [&fake]() { QTest::qWait(20); Q_UNUSED(fake); };
        backend.seedImport = [&fake](const QString &name, const QString &profile) {
            QVariantMap spec;
            spec.insert(QStringLiteral("name"), name);
            spec.insert(QStringLiteral("protocol"), QStringLiteral("vless-reality"));
            spec.insert(QStringLiteral("endpoint"), QStringLiteral("contract.example:443"));
            spec.insert(QStringLiteral("uuid"), QStringLiteral("00000000-0000-0000-0000-000000000000"));
            spec.insert(QStringLiteral("server_name"), QStringLiteral("contract.example"));
            fake.importTunnel(importPayload(name));
            fake.setTunnelProfiles(name, {profile});
        };

        const QStringList failures = runContractRows(backend);
        QVERIFY2(failures.isEmpty(), qPrintable(QStringLiteral("FakeCore violates the target contract:\n  %1")
                                                    .arg(failures.join(QStringLiteral("\n  ")))));
    }

    // The real core runs the SAME rows against a hermetic live core. The
    // create/remove rows are the designed W0 red gate: they fail until W3
    // makes the lifecycle live.
    void realCoreMatchesTargetContract()
    {
        QString coreBinary, toadBinary, error;
        QVERIFY2(UiTest::LiveCoreFixture::resolveBinaries(&coreBinary, &toadBinary, &error),
                 qPrintable(error));
        UiTest::LiveCoreFixture fixture(coreBinary, toadBinary);
        QVERIFY2(fixture.start(&error), qPrintable(error));

        RealCoreClient client(fixture.socketPath(), nullptr);
        QTRY_VERIFY_WITH_TIMEOUT(client.revision() > 0, 5000);
        QVERIFY(client.commandChannelReady());

        SemanticBackend backend;
        backend.lane = QStringLiteral("RealCore");
        backend.core = &client;
        backend.loadedStoppedRole = QStringLiteral("alpha");
        backend.loadedStoppedRoleId = QStringLiteral("alpha");
        backend.loadedRoleProfile = QStringLiteral("home");
        backend.settle = [&client]() {
            // Converge: pump events until the revision stops moving (bounded).
            QElapsedTimer timer;
            timer.start();
            qulonglong last = client.revision();
            qint64 lastChange = 0;
            while (timer.elapsed() < 3000) {
                QTest::qWait(100);
                if (client.revision() != last) {
                    last = client.revision();
                    lastChange = timer.elapsed();
                }
            }
            Q_UNUSED(lastChange);
        };
        backend.seedImport = [&client](const QString &name, const QString &profile) {
            client.importTunnel(importPayload(name));
            client.setTunnelProfiles(name, {profile});
        };

        const QStringList failures = runContractRows(backend);
        QVERIFY2(failures.isEmpty(), qPrintable(QStringLiteral("RealCore violates the target contract "
                                                               "(the W3 red gate):\n  %1")
                                                    .arg(failures.join(QStringLiteral("\n  ")))));
    }
};

QTEST_MAIN(CoreSemanticContractTest)
#include "CoreSemanticContractTest.moc"
