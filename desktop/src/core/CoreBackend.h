#pragma once

#include <QObject>
#include <QString>

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
    Q_PROPERTY(QString underlaySummary READ underlaySummary NOTIFY snapshotChanged)
    Q_PROPERTY(QString aggregateState READ aggregateState NOTIFY snapshotChanged)
    Q_PROPERTY(QString aggregateStateText READ aggregateStateText NOTIFY snapshotChanged)
    Q_PROPERTY(QString aggregateActionText READ aggregateActionText NOTIFY snapshotChanged)
    Q_PROPERTY(qulonglong revision READ revision NOTIFY snapshotChanged)
    Q_PROPERTY(bool leshySupported READ leshySupported CONSTANT)
    Q_PROPERTY(RoleListModel* roles READ roles CONSTANT)

public:
    explicit CoreBackend(QObject *parent = nullptr) : QObject(parent) {}
    ~CoreBackend() override = default;

    virtual QString backendKind() const = 0;
    virtual QString platformName() const = 0;
    virtual QString coreState() const = 0;
    virtual QString activeProfile() const = 0;
    virtual QString underlaySummary() const = 0;
    virtual QString aggregateState() const = 0;
    virtual QString aggregateStateText() const = 0;
    virtual QString aggregateActionText() const = 0;
    virtual qulonglong revision() const = 0;
    virtual bool leshySupported() const = 0;
    virtual RoleListModel *roles() = 0;

    Q_INVOKABLE virtual void toggleAll() = 0;
    Q_INVOKABLE virtual void connectAll() = 0;
    Q_INVOKABLE virtual void disconnectAll() = 0;
    Q_INVOKABLE virtual void roleAction(int row) = 0;
    Q_INVOKABLE virtual void nextDemoScenario() = 0;

signals:
    void snapshotChanged();
};
