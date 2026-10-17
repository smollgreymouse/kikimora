// Live-core tray matrix: the tray menu must mirror the core and drive it.
//
// The fake-backend tray test covers structure; this one proves the same menu
// against the real control plane - state line, per-role lines, and the toggle
// that actually switches the tunnels.
#include "DesktopIntegration.h"
#include "core/RealCoreClient.h"
#include "models/RoleListModel.h"

#include <QAction>
#include <QApplication>
#include <QFileInfo>
#include <QMenu>
#include <QTest>

class TrayLiveTest final : public QObject
{
    Q_OBJECT

private slots:
    void trayMirrorsTheCoreAndDrivesIt();
};

void TrayLiveTest::trayMirrorsTheCoreAndDrivesIt()
{
    const QString socketPath = qEnvironmentVariable("KIKIMORA_UI_SOCKET");
    if (socketPath.isEmpty())
        QSKIP("KIKIMORA_UI_SOCKET is not set: no live core to drive");
    if (!QFileInfo::exists(socketPath))
        QSKIP("KIKIMORA_UI_SOCKET points at a missing socket");

    RealCoreClient core(socketPath, nullptr);
    DesktopIntegration desktop(&core);
    QTRY_VERIFY_WITH_TIMEOUT(core.roles()->rowCount() >= 1, 5000);

    QMenu *menu = desktop.contextMenu();
    QVERIFY(menu);
    const auto labels = [menu] {
        QStringList out;
        const QList<QAction *> actions = menu->actions();
        for (QAction *action : actions)
            if (!action->isSeparator())
                out << action->text();
        return out;
    };

    // The first, disabled line mirrors the aggregate state the window shows.
    const QString aggregate = core.aggregateStateText().isEmpty() ? core.coreState()
                                                                  : core.aggregateStateText();
    QVERIFY2(labels().contains(aggregate),
             qPrintable(QStringLiteral("state line %1 missing from [%2]")
                            .arg(aggregate, labels().join(QStringLiteral(" | ")))));
    QVERIFY(!menu->actions().first()->isEnabled());

    // One informational line per role, carrying that role's name.
    for (int row = 0; row < core.roles()->rowCount(); ++row) {
        const RoleSnapshot role = core.roles()->roleAt(row);
        const QString name = role.label.isEmpty() ? role.id : role.label;
        bool found = false;
        for (const QString &text : labels())
            if (text.contains(name)) { found = true; break; }
        QVERIFY2(found, qPrintable(QStringLiteral("no tray line for %1 in [%2]")
                                       .arg(name, labels().join(QStringLiteral(" | ")))));
    }

    // The toggle follows the core's intent and actually drives it.
    QAction *toggle = nullptr;
    const QList<QAction *> actions = menu->actions();
    for (QAction *action : actions)
        if (action->text().contains(QStringLiteral("all tunnels"))) { toggle = action; break; }
    QVERIFY(toggle);
    QVERIFY(toggle->isEnabled());

    const QString before = core.aggregateState();
    toggle->trigger();
    QTRY_VERIFY_WITH_TIMEOUT(core.aggregateState() != before, 90000);
    QTRY_VERIFY_WITH_TIMEOUT(core.aggregateStateText() == aggregate || !core.aggregateStateText().isEmpty(), 5000);
    const bool wantsDisconnect = core.aggregateActionText() == QStringLiteral("DISCONNECT");
    QVERIFY2(toggle->text().contains(wantsDisconnect ? QStringLiteral("Disconnect all")
                                                     : QStringLiteral("Connect all")),
             qPrintable(QStringLiteral("toggle text '%1' does not follow the core").arg(toggle->text())));

    // Leave the host as we found it: trigger the same action again.
    QTRY_VERIFY_WITH_TIMEOUT(toggle->isEnabled(), 10000);
    toggle->trigger();
    QTRY_VERIFY_WITH_TIMEOUT(core.aggregateState() == before, 120000);
}

QTEST_MAIN(TrayLiveTest)
#include "TrayLiveTest.moc"
