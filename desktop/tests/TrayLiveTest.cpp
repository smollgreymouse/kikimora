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
// vtable is generated for the target that only lists the .cpp.
class TrayLiveTest final : public QObject
{
    Q_OBJECT

private slots:
    void initTestCase();
    // Reading the menu against the live core: no state changes, so this is quick.
    void trayMirrorsTheCore();
    // The toggle path: one disconnect/connect cycle from a known base state.
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
    const QString aggregate = core.aggregateStateText().isEmpty() ? core.aggregateState()
                                                                  : core.aggregateStateText();
    QVERIFY2(stateAction->text() == aggregate ||
                 stateAction->text().contains(aggregate, Qt::CaseInsensitive),
             qPrintable(QStringLiteral("state line '%1' does not follow aggregate '%2'")
                            .arg(stateAction->text(), aggregate)));

    // The menu carries one line per role on top of the state line, the toggle, show and
    // quit. TrayMenuTest pins the exact texts against the fake backend; here the live
    // requirement is that every role the core reports is represented.
    QVERIFY2(menu->actions().size() >= 4 + core.roles()->rowCount(),
             qPrintable(QStringLiteral("menu has %1 actions for %2 roles")
                            .arg(menu->actions().size())
                            .arg(core.roles()->rowCount())));
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

    // Put the core in a known base state through the client: a toggle can only be
    // asserted against a state it is guaranteed to change. Repeated runs leave the
    // guest mid-recovery, where DisconnectAll is a no-op.
    const QString entry = core.aggregateState();
    core.disconnectAll();
    const bool stopped = QTest::qWaitFor([&core] {
        return core.aggregateState() == QStringLiteral("Stopped");
    }, 60000);
    if (!stopped)
        QSKIP("the core does not settle on Stopped, nothing to toggle from");

    const QString before = core.aggregateState();
    QAction *toggle = findAction(menu, [](QAction *action) {
        return action->text().contains(QStringLiteral("Connect all")) ||
               action->text().contains(QStringLiteral("Disconnect all"));
    });
    QVERIFY2(toggle, "the tray has no aggregate toggle");
    QTRY_VERIFY_WITH_TIMEOUT(toggle->isEnabled(), 10000);
    QVERIFY2(toggle->text().contains(QStringLiteral("Connect all")),
             qPrintable(QStringLiteral("toggle text '%1' should offer a connect from Stopped")
                            .arg(toggle->text())));

    toggle->trigger();
    QTRY_VERIFY_WITH_TIMEOUT(core.aggregateState() != before, 60000);
    QTRY_VERIFY_WITH_TIMEOUT(toggle->text().contains(QStringLiteral("Disconnect all")), 30000);

    // Leave the host the way it was found: the lab baseline is "roles connected", and
    // the live suites that run after this one depend on it.
    if (entry != QStringLiteral("Stopped")) {
        core.connectAll();
        QTRY_VERIFY_WITH_TIMEOUT(core.aggregateState() != QStringLiteral("Stopped"), 120000);
    } else {
        core.disconnectAll();
        QTRY_VERIFY_WITH_TIMEOUT(core.aggregateState() == QStringLiteral("Stopped"), 60000);
    }
}

QTEST_MAIN(TrayLiveTest)
#include "TrayLiveTest.moc"
