#include <QAction>
#include <QFileInfo>
#include <QMenu>
#include <QProcessEnvironment>
#include <QTest>

#include <functional>

#include "core/RealCoreClient.h"
#include "DesktopIntegration.h"

namespace {

QAction *findAction(QMenu *menu, const std::function<bool(QAction *)> &predicate)
{
    const QList<QAction *> actions = menu->actions();
    for (QAction *action : actions) {
        if (action && predicate(action)) return action;
    }
    return nullptr;
}

} // namespace

// The class lives here rather than in a header: AutoMoc then runs on this file and the
// vtable is guaranteed to be generated for the target that only lists the .cpp.
class TrayLiveTest final : public QObject
{
    Q_OBJECT

private slots:
    void initTestCase();
    // Reading the menu against the live core: no state changes, so this is quick.
    void trayMirrorsTheCore();
    // The toggle path: one disconnect/connect cycle, budgeted per test function.
    void trayToggleDrivesTheCore();

private:
    QString m_socketPath;
};

void TrayLiveTest::initTestCase()
{
    m_socketPath = QProcessEnvironment::systemEnvironment().value("KIKIMORA_UI_SOCKET").trimmed();
    if (m_socketPath.isEmpty() || !QFileInfo::exists(m_socketPath))
        QSKIP("KIKIMORA_UI_SOCKET does not point at a live core socket");
}

void TrayLiveTest::trayMirrorsTheCore()
{
    RealCoreClient core(m_socketPath, nullptr);
    DesktopIntegration desktop(&core);

    const bool armed = QTest::qWaitFor([&core] {
        return core.roles() && core.roles()->rowCount() >= 1 && !core.coreState().isEmpty();
    }, 30000);
    QVERIFY2(armed, "the core did not publish its state");

    QMenu *menu = desktop.contextMenu();
    QVERIFY(menu);

    // The first line is the aggregate state, rendered as a disabled label.
    QAction *stateAction = menu->actions().value(0);
    QVERIFY2(stateAction, "the tray has no state line");
    QVERIFY2(!stateAction->isEnabled(), "the state line must be informational");
    QVERIFY2(!stateAction->text().isEmpty(), "the state line is empty");
    const QString aggregate = core.aggregateStateText().isEmpty() ? core.aggregateState()
                                                                  : core.aggregateStateText();
    QVERIFY2(stateAction->text().contains(aggregate, Qt::CaseInsensitive) ||
                 aggregate.contains(stateAction->text(), Qt::CaseInsensitive),
             qPrintable(QStringLiteral("state line '%1' does not follow aggregate '%2'")
                            .arg(stateAction->text(), aggregate)));

    for (int row = 0; row < core.roles()->rowCount(); ++row) {
        const QModelIndex index = core.roles()->index(row, 0);
        const QString name = core.roles()->data(index, Qt::DisplayRole).toString();
        QVERIFY2(!name.isEmpty(), "a role arrived without a name");
        QAction *line = findAction(menu, [&name](QAction *action) {
            return action->text() != name && action->text().startsWith(name);
        });
        QVERIFY2(line, qPrintable(QStringLiteral("no tray line for role %1").arg(name)));
    }
}

void TrayLiveTest::trayToggleDrivesTheCore()
{
    RealCoreClient core(m_socketPath, nullptr);
    DesktopIntegration desktop(&core);

    const bool armed = QTest::qWaitFor([&core] {
        return core.roles() && core.roles()->rowCount() >= 1 && !core.coreState().isEmpty();
    }, 30000);
    QVERIFY2(armed, "the core did not publish its state");

    QMenu *menu = desktop.contextMenu();
    QVERIFY(menu);

    // A previous run may leave the core mid-recovery; the toggle asserts on a settled
    // Ready/Stopped state.
    // Settled here means "two consecutive readings agree": the guest is often still
    // recovering from a previous run, and the toggle only has to change that state.
    QString before;
    for (int attempt = 0; attempt < 24; ++attempt) {
        const QString first = core.aggregateState();
        QTest::qWait(5000);
        if (core.aggregateState() == first) {
            before = first;
            break;
        }
    }
    QVERIFY2(!before.isEmpty(), "the core never settled on a state");
    const bool wantsDisconnect = core.aggregateActionText() == QStringLiteral("DISCONNECT");

    QAction *toggle = findAction(menu, [](QAction *action) {
        return action->text().contains(QStringLiteral("Connect all")) ||
               action->text().contains(QStringLiteral("Disconnect all"));
    });
    QVERIFY2(toggle, "the tray has no aggregate toggle");
    QTRY_VERIFY_WITH_TIMEOUT(toggle->isEnabled(), 10000);
    QVERIFY2(toggle->text().contains(wantsDisconnect ? QStringLiteral("Disconnect all")
                                                     : QStringLiteral("Connect all")),
             qPrintable(QStringLiteral("toggle text %1 does not follow DOWN/CONNECT").arg(toggle->text())));

    toggle->trigger();
    QTRY_VERIFY_WITH_TIMEOUT(core.aggregateState() != before, 60000);

    QTRY_VERIFY_WITH_TIMEOUT(core.aggregateStateText() == core.aggregateState() ||
                                 !core.aggregateStateText().isEmpty(),
                             20000);
    QTRY_VERIFY_WITH_TIMEOUT(toggle->text().contains(wantsDisconnect ? QStringLiteral("Connect all")
                                                                   : QStringLiteral("Disconnect all")),
                             20000);

    // Restore through the core: a second UI trigger costs another full three-role
    // reconnect, which is the expensive part of this test.
    if (before == QStringLiteral("Ready")) {
        core.connectAll();
    } else {
        core.disconnectAll();
    }
    QTRY_VERIFY_WITH_TIMEOUT(core.aggregateState() == before, 90000);
}

QTEST_MAIN(TrayLiveTest)
#include "TrayLiveTest.moc"
