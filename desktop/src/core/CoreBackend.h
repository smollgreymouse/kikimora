#pragma once

#include <QObject>
#include <QString>
#include <QVariantMap>

#include "models/RoleListModel.h"
#include "core/TunnelImport.h"


// UI-facing contract for the future Kikimora core connection.
//
// FakeCoreBackend implements it in this prototype. A future LocalIpcBackend should
// implement the same surface and translate versioned Go-core snapshots/events into
// these presentation properties. QML must not depend on FakeCoreBackend directly.
class CoreBackend : public QObject
{
    Q_OBJECT
    Q_PROPERTY(QString backendKind READ backendKind CONSTANT)
    Q_PROPERTY(QString platformName READ platformName CONSTANT)
    Q_PROPERTY(QString coreState READ coreState NOTIFY snapshotChanged)
    Q_PROPERTY(QString activeProfile READ activeProfile NOTIFY snapshotChanged)
    Q_PROPERTY(QStringList profiles READ profiles NOTIFY snapshotChanged)
    Q_PROPERTY(QString underlaySummary READ underlaySummary NOTIFY snapshotChanged)
    Q_PROPERTY(QVariantMap underlay READ underlay NOTIFY snapshotChanged)
    Q_PROPERTY(QString lastCommandError READ lastCommandError NOTIFY snapshotChanged)
    Q_PROPERTY(QString aggregateState READ aggregateState NOTIFY snapshotChanged)
    Q_PROPERTY(QString aggregateStateText READ aggregateStateText NOTIFY snapshotChanged)
    Q_PROPERTY(QString aggregateActionText READ aggregateActionText NOTIFY snapshotChanged)
    Q_PROPERTY(qulonglong revision READ revision NOTIFY snapshotChanged)
    Q_PROPERTY(bool leshySupported READ leshySupported NOTIFY snapshotChanged)
    Q_PROPERTY(bool simulated READ simulated NOTIFY snapshotChanged)
    Q_PROPERTY(RoleListModel* roles READ roles CONSTANT)
    Q_PROPERTY(QString lastCommandMessage READ lastCommandMessage NOTIFY snapshotChanged)
    // Current Leshy routing lists, keyed by primary/secondary/bypass.
    Q_PROPERTY(QVariantMap routingLists READ routingLists NOTIFY snapshotChanged)
    // Leshy zones: which device each routing list is carried through.
    Q_PROPERTY(QVariantList routingZones READ routingZones NOTIFY snapshotChanged)
    // Global rule for traffic no role claims: "direct" or a role that carries it.
    Q_PROPERTY(QString bypassMode READ bypassMode NOTIFY snapshotChanged)
    // Role labels as a plain list: a QML binding over a model function is evaluated once and
    // never follows later snapshots, which left the routing page without roles.
    Q_PROPERTY(QStringList roleLabels READ roleLabels NOTIFY snapshotChanged)
    // Kernel-observer health from the core snapshot (netlink, converger, sleep,
    // NetworkManager) with the last error of each.
    Q_PROPERTY(QVariantMap observers READ observers NOTIFY snapshotChanged)
    // One-shot GetDiagnosticsSnapshot payload: versions, platform, socket,
    // managed interfaces, desired-state status.
    Q_PROPERTY(QVariantMap diagnostics READ diagnostics NOTIFY snapshotChanged)

public:
    explicit CoreBackend(QObject *parent = nullptr) : QObject(parent) {}
    ~CoreBackend() override = default;

    virtual QString backendKind() const = 0;
    virtual QString platformName() const = 0;
    virtual QString coreState() const = 0;
    virtual QString activeProfile() const = 0;
    virtual QStringList profiles() const = 0;
    virtual QString underlaySummary() const = 0;
    virtual QVariantMap underlay() const = 0;
    virtual QString lastCommandError() const = 0;
    virtual QString aggregateState() const = 0;
    virtual QString aggregateStateText() const = 0;
    virtual QString aggregateActionText() const = 0;
    virtual qulonglong revision() const = 0;
    virtual bool leshySupported() const = 0;
    // True when the UI is backed by a simulated core (FakeCore or the Windows
    // prototype path). Drives the visible SIMULATED marker so the UI never
    // implies the host is actually protected.
    virtual bool simulated() const = 0;
    virtual RoleListModel *roles() = 0;
    // What the last file-changing command did, for the create flows in the UI.
    virtual QString lastCommandMessage() const = 0;
    virtual QVariantMap routingLists() const = 0;
    virtual QVariantList routingZones() const = 0;
    virtual QString bypassMode() const = 0;
    virtual QStringList roleLabels() const = 0;
    virtual QVariantMap observers() const = 0;
    virtual QVariantMap diagnostics() const = 0;
    // One explicit GetDiagnosticsSnapshot request; the settings card calls it
    // when the operator expands the diagnostics section.
    Q_INVOKABLE virtual void refreshDiagnostics() = 0;
    Q_INVOKABLE virtual void setBypassMode(const QString &mode) = 0;
    // Profile membership of a role, as the backend knows it. The fake keeps it in memory,
    // the real client cannot read it back without parsing configurations, so it answers
    // with the last membership it was told about.
    Q_INVOKABLE virtual QStringList profilesForRole(const QString &name) const = 0;

    Q_INVOKABLE virtual void toggleAll() = 0;
    Q_INVOKABLE virtual void connectAll() = 0;
    Q_INVOKABLE virtual void disconnectAll() = 0;
    Q_INVOKABLE virtual void roleAction(int row) = 0;
    // Per-role action selected from the core-reported available_actions list.
    Q_INVOKABLE virtual void roleActionByName(int row, const QString &action) = 0;
    Q_INVOKABLE virtual void retryRole(int row) = 0;
    Q_INVOKABLE virtual void validateRole(int row) = 0;
    Q_INVOKABLE virtual void rediscoverEndpoints(int row) = 0;
    Q_INVOKABLE virtual void nextDemoScenario() = 0;
    Q_INVOKABLE virtual void setActiveProfile(const QString &name) = 0;
    // Write a new tunnel or profile through the core. Both only change configuration on
    // the core host: the running composition picks them up after a core restart, which
    // the core reports back through lastCommandMessage.
    Q_INVOKABLE virtual void createTunnel(const QVariantMap &spec) = 0;
    // A profile is an explicit set of roles: a new one starts empty unless roles are
    // passed here. A role may be named by several profiles.
    Q_INVOKABLE virtual void createProfile(const QString &name, const QStringList &roles) = 0;
    // Replace the profile membership of a tunnel: the move path between profiles.
    Q_INVOKABLE virtual void setTunnelProfiles(const QString &name, const QStringList &profiles) = 0;
    // One import field: classify what was pasted, then build the tunnel from it.
    Q_INVOKABLE QString describeTunnelImport(const QString &text) const;
    // The clipboard lives on DesktopIntegration: the core backend contract is linked by
    // targets that carry QtCore only.
    Q_INVOKABLE virtual void importTunnel(const QString &text) = 0;
    // Delete a tunnel configuration the core wrote; the role disappears after a restart.
    Q_INVOKABLE virtual void removeTunnel(const QString &name) = 0;
    Q_INVOKABLE virtual void refreshRoutingLists() = 0;
    Q_INVOKABLE virtual void saveRoutingList(const QString &name, const QString &content) = 0;
    // A role feeds one Leshy list; the core writes it into the role configuration.

signals:
    void snapshotChanged();
    // Result of createProfile. The core writes the profile configuration and only
    // loads it on the next start, so the UI must not guess success: it reports
    // what the core answered and whether the profile is in the live list yet.
    void profileCreationFinished(const QString &name, bool ok, const QString &message);
};
