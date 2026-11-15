#include "models/RoleListModel.h"

#include <QAbstractItemModelTester>
#include <QSignalSpy>
#include <QTest>

namespace {
RoleSnapshot makeRole(const QString &id, const QString &state)
{
    RoleSnapshot role;
    role.id = id;
    role.label = id;
    role.protocol = QStringLiteral("AmneziaWG");
    role.server = QStringLiteral("NL - Amsterdam");
    role.state = state;
    role.stateText = state;
    role.interfaceName = QStringLiteral("amn0");
    role.underlay = QStringLiteral("wlan0 via 192.168.1.1");
    role.endpointState = QStringLiteral("ready");
    role.leshyPublished = state != QStringLiteral("Stopped");
    role.parkedRoutes = 3;
    role.lastRecovery = QStringLiteral("Manual per-role connect");
    role.lastError = QStringLiteral("-");
    return role;
}

// A fully populated snapshot: every field the data() switch can return carries a
// distinct, assertable value, so one pass over every role exercises the whole
// mapping (U11: RoleListModel.cpp line coverage).
RoleSnapshot makeFullRole(const QString &id)
{
    RoleSnapshot role = makeRole(id, QStringLiteral("Ready"));
    role.desiredEnabled = true;
    role.operation = 7;
    role.validatedUnderlayEpoch = 9;
    role.recoveryAction = QStringLiteral("Retry per-role connect");
    role.recoveryReason = QStringLiteral("handoff timeout");
    role.recoveryAttempt = 2;
    role.nextRetryAt = QStringLiteral("2026-11-15T19:00:00Z");
    role.validationState = QStringLiteral("ready");
    role.validationReason = QStringLiteral("managed route target is structurally ready");
    role.configuredEndpoints = {QStringLiteral("10.8.0.1:51820"), QStringLiteral("10.8.0.2:51820")};
    role.liveEndpoints = {QStringLiteral("10.8.0.1:51820")};
    role.leshyZone = QStringLiteral("awg");
    role.leshyIpv6 = false;
    role.parkingActive = true;
    role.routeReady = true;
    role.interfaceIndex = 6;
    role.interfaceMtu = 1380;
    role.sessionConnected = true;
    role.sessionRxBytes = 1048576.0;
    role.sessionTxBytes = 262144.0;
    role.sessionEndpoint = QStringLiteral("10.8.0.1:51820");
    role.lastHandshakeAgeMs = 1250;
    role.resolvedEndpoints = {QStringLiteral("203.0.113.10:51820")};
    role.endpointLastError = QStringLiteral("none");
    role.appliedUnderlayEpoch = 9;
    role.parkedPrefixes = {QStringLiteral("203.0.113.0/24")};
    role.pid = 4242;
    role.generation = 7;
    role.availableActions = {QStringLiteral("disconnect")};
    role.ipv4Configured = true;
    role.ipv4Ready = true;
    role.ipv4Reason = QStringLiteral("ready");
    role.ipv6Configured = false;
    role.ipv6Ready = false;
    role.ipv6Reason = QStringLiteral("slaac pending");
    role.selectedRoutes = {{QStringLiteral("registered"), 2}, {QStringLiteral("active"), 2}};
    return role;
}
}

class RoleListModelTest final : public QObject
{
    Q_OBJECT

private slots:
    void staysConsistentThroughStructuralChanges()
    {
        RoleListModel model;
        QAbstractItemModelTester tester(&model);
        model.setRoles({makeRole(QStringLiteral("primary"), QStringLiteral("Stopped")),
                        makeRole(QStringLiteral("secondary"), QStringLiteral("Ready"))});
        QCOMPARE(model.rowCount(), 2);
        model.updateRole(1, makeRole(QStringLiteral("secondary"), QStringLiteral("Failed")));
        model.addRole(makeRole(QStringLiteral("tertiary"), QStringLiteral("Stopped")));
        QCOMPARE(model.rowCount(), 3);
        model.clear();
        QCOMPARE(model.rowCount(), 0);
    }

    // Every role in the model's data() switch must return its own field. A single
    // fully populated snapshot read through every role name covers the mapping
    // without inventing behaviour: each assertion compares against the value
    // makeFullRole() put in.
    void dataMapsEverySnapshotField()
    {
        RoleListModel model;
        model.setRoles({makeFullRole(QStringLiteral("primary"))});
        const QModelIndex idx = model.index(0, 0);
        QVERIFY(idx.isValid());

        QCOMPARE(idx.data(RoleListModel::IdRole).toString(), QStringLiteral("primary"));
        QCOMPARE(idx.data(RoleListModel::LabelRole).toString(), QStringLiteral("primary"));
        QCOMPARE(idx.data(RoleListModel::ProtocolRole).toString(), QStringLiteral("AmneziaWG"));
        QCOMPARE(idx.data(RoleListModel::ServerRole).toString(), QStringLiteral("NL - Amsterdam"));
        QCOMPARE(idx.data(RoleListModel::StateRole).toString(), QStringLiteral("Ready"));
        QCOMPARE(idx.data(RoleListModel::DesiredEnabledRole).toBool(), true);
        QCOMPARE(idx.data(RoleListModel::OperationRole).toULongLong(), 7ULL);
        QCOMPARE(idx.data(RoleListModel::ValidatedUnderlayEpochRole).toULongLong(), 9ULL);
        QCOMPARE(idx.data(RoleListModel::StateTextRole).toString(), QStringLiteral("Ready"));
        QCOMPARE(idx.data(RoleListModel::InterfaceRole).toString(), QStringLiteral("amn0"));
        QCOMPARE(idx.data(RoleListModel::UnderlayRole).toString(),
                 QStringLiteral("wlan0 via 192.168.1.1"));
        QCOMPARE(idx.data(RoleListModel::EndpointStateRole).toString(), QStringLiteral("ready"));
        QCOMPARE(idx.data(RoleListModel::LeshyPublishedRole).toBool(), true);
        QCOMPARE(idx.data(RoleListModel::ParkedRoutesRole).toInt(), 3);
        QCOMPARE(idx.data(RoleListModel::LastRecoveryRole).toString(),
                 QStringLiteral("Manual per-role connect"));
        QCOMPARE(idx.data(RoleListModel::LastErrorRole).toString(), QStringLiteral("-"));
        QCOMPARE(idx.data(RoleListModel::RecoveryActionRole).toString(),
                 QStringLiteral("Retry per-role connect"));
        QCOMPARE(idx.data(RoleListModel::RecoveryReasonRole).toString(),
                 QStringLiteral("handoff timeout"));
        QCOMPARE(idx.data(RoleListModel::RecoveryAttemptRole).toInt(), 2);
        QCOMPARE(idx.data(RoleListModel::NextRetryAtRole).toString(),
                 QStringLiteral("2026-11-15T19:00:00Z"));
        QCOMPARE(idx.data(RoleListModel::ValidationStateRole).toString(), QStringLiteral("ready"));
        QCOMPARE(idx.data(RoleListModel::ValidationReasonRole).toString(),
                 QStringLiteral("managed route target is structurally ready"));
        QCOMPARE(idx.data(RoleListModel::ConfiguredEndpointsRole).toStringList().size(), 2);
        QCOMPARE(idx.data(RoleListModel::LiveEndpointsRole).toStringList().size(), 1);
        QCOMPARE(idx.data(RoleListModel::LeshyZoneRole).toString(), QStringLiteral("awg"));
        QCOMPARE(idx.data(RoleListModel::LeshyIpv6Role).toBool(), false);
        QCOMPARE(idx.data(RoleListModel::ParkingActiveRole).toBool(), true);
        QCOMPARE(idx.data(RoleListModel::RouteReadyRole).toBool(), true);
        QCOMPARE(idx.data(RoleListModel::InterfaceIndexRole).toInt(), 6);
        QCOMPARE(idx.data(RoleListModel::InterfaceMtuRole).toInt(), 1380);
        QCOMPARE(idx.data(RoleListModel::SessionConnectedRole).toBool(), true);
        QCOMPARE(idx.data(RoleListModel::SessionRxBytesRole).toDouble(), 1048576.0);
        QCOMPARE(idx.data(RoleListModel::SessionTxBytesRole).toDouble(), 262144.0);
        QCOMPARE(idx.data(RoleListModel::SessionEndpointRole).toString(),
                 QStringLiteral("10.8.0.1:51820"));
        QCOMPARE(idx.data(RoleListModel::LastHandshakeAgeMsRole).toLongLong(), 1250LL);
        QCOMPARE(idx.data(RoleListModel::ResolvedEndpointsRole).toStringList().size(), 1);
        QCOMPARE(idx.data(RoleListModel::EndpointLastErrorRole).toString(), QStringLiteral("none"));
        QCOMPARE(idx.data(RoleListModel::AppliedUnderlayEpochRole).toULongLong(), 9ULL);
        QCOMPARE(idx.data(RoleListModel::ParkedPrefixesRole).toStringList().size(), 1);
        QCOMPARE(idx.data(RoleListModel::PidRole).toInt(), 4242);
        QCOMPARE(idx.data(RoleListModel::GenerationRole).toULongLong(), 7ULL);
        QCOMPARE(idx.data(RoleListModel::AvailableActionsRole).toStringList(),
                 QStringList{QStringLiteral("disconnect")});
        QCOMPARE(idx.data(RoleListModel::Ipv4ConfiguredRole).toBool(), true);
        QCOMPARE(idx.data(RoleListModel::Ipv4ReadyRole).toBool(), true);
        QCOMPARE(idx.data(RoleListModel::Ipv4ReasonRole).toString(), QStringLiteral("ready"));
        QCOMPARE(idx.data(RoleListModel::Ipv6ConfiguredRole).toBool(), false);
        QCOMPARE(idx.data(RoleListModel::Ipv6ReadyRole).toBool(), false);
        QCOMPARE(idx.data(RoleListModel::Ipv6ReasonRole).toString(), QStringLiteral("slaac pending"));
        const QVariantMap routes = idx.data(RoleListModel::SelectedRoutesRole).toMap();
        QCOMPARE(routes.value(QStringLiteral("registered")).toInt(), 2);

        // An out-of-range role must return an empty variant rather than crash.
        QVERIFY(!idx.data(-1).isValid());
    }

    void dataExposesSnapshotFieldsWithStableRoleNames()
    {        RoleListModel model;
        model.setRoles({makeRole(QStringLiteral("primary"), QStringLiteral("Ready"))});

        const auto names = model.roleNames();
        QCOMPARE(names.value(RoleListModel::IdRole), QByteArray("roleId"));
        QCOMPARE(names.value(RoleListModel::LabelRole), QByteArray("label"));
        QCOMPARE(names.value(RoleListModel::StateTextRole), QByteArray("stateText"));
        QCOMPARE(names.value(RoleListModel::InterfaceRole), QByteArray("interfaceName"));
        QCOMPARE(names.value(RoleListModel::LeshyPublishedRole), QByteArray("leshyPublished"));
        QCOMPARE(names.value(RoleListModel::AvailableActionsRole), QByteArray("availableActions"));

        const QModelIndex idx = model.index(0, 0);
        QCOMPARE(idx.data(RoleListModel::LabelRole).toString(), QStringLiteral("primary"));
        QCOMPARE(idx.data(RoleListModel::StateRole).toString(), QStringLiteral("Ready"));
        QCOMPARE(idx.data(RoleListModel::StateTextRole).toString(), QStringLiteral("Ready"));
        QCOMPARE(idx.data(RoleListModel::InterfaceRole).toString(), QStringLiteral("amn0"));
        QCOMPARE(idx.data(RoleListModel::LeshyPublishedRole).toBool(), true);
        QCOMPARE(idx.data(RoleListModel::ParkedRoutesRole).toInt(), 3);
        QCOMPARE(idx.data(RoleListModel::LastRecoveryRole).toString(), QStringLiteral("Manual per-role connect"));
        QCOMPARE(idx.data(RoleListModel::ParkingActiveRole).toBool(), false);
        QVERIFY(!model.index(1, 0).isValid());
    }

    void updateRoleEmitsDataChangedOnlyForExistingRows()
    {
        RoleListModel model;
        model.setRoles({makeRole(QStringLiteral("a"), QStringLiteral("Stopped")),
                        makeRole(QStringLiteral("b"), QStringLiteral("Stopped"))});
        QSignalSpy spy(&model, &QAbstractItemModel::dataChanged);

        model.updateRole(1, makeRole(QStringLiteral("b"), QStringLiteral("Ready")));
        QCOMPARE(spy.size(), 1);
        const auto args = spy.constFirst();
        QCOMPARE(args.at(0).toModelIndex().row(), 1);
        QCOMPARE(args.at(1).toModelIndex().row(), 1);
        QCOMPARE(model.roleAt(1).state, QStringLiteral("Ready"));

        model.updateRole(-1, makeRole(QStringLiteral("x"), QStringLiteral("Ready")));
        model.updateRole(99, makeRole(QStringLiteral("x"), QStringLiteral("Ready")));
        QCOMPARE(spy.size(), 1);
        QCOMPARE(model.roleAt(1).state, QStringLiteral("Ready"));
    }

    void getReturnsFullMapAndGuardsBounds()
    {
        RoleListModel model;
        model.setRoles({makeRole(QStringLiteral("a"), QStringLiteral("Stopped"))});

        const QVariantMap map = model.get(0);
        QCOMPARE(map.value(QStringLiteral("roleId")).toString(), QStringLiteral("a"));
        QCOMPARE(map.value(QStringLiteral("state")).toString(), QStringLiteral("Stopped"));
        QCOMPARE(map.value(QStringLiteral("interfaceName")).toString(), QStringLiteral("amn0"));
        QCOMPARE(map.value(QStringLiteral("leshyPublished")).toBool(), false);
        QCOMPARE(map.value(QStringLiteral("parkedRoutes")).toInt(), 3);
        QVERIFY(map.contains(QStringLiteral("lastRecovery")));
        QVERIFY(map.contains(QStringLiteral("lastError")));
        QVERIFY(map.contains(QStringLiteral("configuredEndpoints")));
        QVERIFY(map.contains(QStringLiteral("availableActions")));

        QVERIFY(model.get(-1).isEmpty());
        QVERIFY(model.get(1).isEmpty());
    }

    void addRoleAndClearEmitStructuralNotifications()
    {
        RoleListModel model;
        QSignalSpy inserted(&model, &QAbstractItemModel::rowsInserted);
        QSignalSpy reset(&model, &QAbstractItemModel::modelReset);

        model.addRole(makeRole(QStringLiteral("a"), QStringLiteral("Stopped")));
        model.addRole(makeRole(QStringLiteral("b"), QStringLiteral("Ready")));
        QCOMPARE(model.rowCount(), 2);
        QCOMPARE(inserted.size(), 2);

        model.clear();
        QCOMPARE(model.rowCount(), 0);
        QCOMPARE(reset.size(), 1);

        model.clear();
        QCOMPARE(reset.size(), 1);
    }

    // --- U1 model contract (roadmap §8) ---

    // The role drawer reads Core.roles.get(index); a missing leshyIpv6 key made
    // the drawer default to "enabled" while the core reported leshy_ipv6=false.
    void getCarriesLeshyIpv6()
    {
        RoleListModel model;
        RoleSnapshot role = makeRole(QStringLiteral("a"), QStringLiteral("Ready"));
        role.leshyIpv6 = false;
        model.setRoles({role});

        QCOMPARE(model.get(0).value(QStringLiteral("leshyIpv6")).toBool(), false);
        QCOMPARE(model.index(0, 0).data(RoleListModel::LeshyIpv6Role).toBool(), false);

        role.leshyIpv6 = true;
        model.setRoles({role});
        QCOMPARE(model.get(0).value(QStringLiteral("leshyIpv6")).toBool(), true);
    }

    void getCarriesFamilyReadiness()
    {
        RoleListModel model;
        RoleSnapshot role = makeRole(QStringLiteral("a"), QStringLiteral("Ready"));
        role.routeReady = true; // legacy bit: one usable family, not both
        role.ipv4Configured = true;
        role.ipv4Ready = true;
        role.ipv6Configured = true;
        role.ipv6Ready = false;
        role.ipv6Reason = QStringLiteral("v6 route pending");
        model.setRoles({role});

        const QVariantMap map = model.get(0);
        QCOMPARE(map.value(QStringLiteral("ipv4Configured")).toBool(), true);
        QCOMPARE(map.value(QStringLiteral("ipv4Ready")).toBool(), true);
        QCOMPARE(map.value(QStringLiteral("ipv6Configured")).toBool(), true);
        QCOMPARE(map.value(QStringLiteral("ipv6Ready")).toBool(), false);
        QCOMPARE(map.value(QStringLiteral("ipv6Reason")).toString(), QStringLiteral("v6 route pending"));
        const QModelIndex idx = model.index(0, 0);
        QCOMPARE(idx.data(RoleListModel::Ipv4ReadyRole).toBool(), true);
        QCOMPARE(idx.data(RoleListModel::Ipv6ReadyRole).toBool(), false);
        QCOMPARE(idx.data(RoleListModel::Ipv6ReasonRole).toString(), QStringLiteral("v6 route pending"));
    }

    void getCarriesSelectedRouteStats()
    {
        RoleListModel model;
        RoleSnapshot role = makeRole(QStringLiteral("a"), QStringLiteral("Ready"));
        role.selectedRoutes = QVariantMap{
            {QStringLiteral("registered"), 3},
            {QStringLiteral("conflicts"), 1},
            {QStringLiteral("denials"), 4},
            {QStringLiteral("kernel_mutations"), 9}};
        model.setRoles({role});

        const QVariantMap stats = model.get(0).value(QStringLiteral("selectedRoutes")).toMap();
        QCOMPARE(stats.value(QStringLiteral("registered")).toInt(), 3);
        QCOMPARE(stats.value(QStringLiteral("conflicts")).toInt(), 1);
        QCOMPARE(stats.value(QStringLiteral("denials")).toInt(), 4);
        QCOMPARE(stats.value(QStringLiteral("kernel_mutations")).toInt(), 9);
        const QVariantMap fromData =
            model.index(0, 0).data(RoleListModel::SelectedRoutesRole).toMap();
        QCOMPARE(fromData.value(QStringLiteral("denials")).toInt(), 4);
    }

    void roleNamesExposeNewRoutingFields()
    {
        RoleListModel model;
        model.setRoles({makeRole(QStringLiteral("a"), QStringLiteral("Stopped"))});
        const auto names = model.roleNames();
        QCOMPARE(names.value(RoleListModel::Ipv4ConfiguredRole), QByteArray("ipv4Configured"));
        QCOMPARE(names.value(RoleListModel::Ipv4ReadyRole), QByteArray("ipv4Ready"));
        QCOMPARE(names.value(RoleListModel::Ipv4ReasonRole), QByteArray("ipv4Reason"));
        QCOMPARE(names.value(RoleListModel::Ipv6ConfiguredRole), QByteArray("ipv6Configured"));
        QCOMPARE(names.value(RoleListModel::Ipv6ReadyRole), QByteArray("ipv6Ready"));
        QCOMPARE(names.value(RoleListModel::Ipv6ReasonRole), QByteArray("ipv6Reason"));
        QCOMPARE(names.value(RoleListModel::SelectedRoutesRole), QByteArray("selectedRoutes"));
    }
};

QTEST_GUILESS_MAIN(RoleListModelTest)
#include "RoleListModelTest.moc"
