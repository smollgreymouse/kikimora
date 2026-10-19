#pragma once

#include <QObject>
#include <QString>
#include <QVariantMap>

#include "models/RoleListModel.h"

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
    Q_INVOKABLE virtual void createProfile(const QString &name) = 0;
    // Replace the profile membership of a tunnel: the move path between profiles.
    Q_INVOKABLE virtual void setTunnelProfiles(const QString &name, const QStringList &profiles) = 0;

signals:
    void snapshotChanged();
};
