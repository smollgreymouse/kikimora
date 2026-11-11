#include "DesktopIntegration.h"

#include "core/FakeCoreBackend.h"
#include "models/RoleListModel.h"

#include <QAction>
#include <QCoreApplication>
#include <QMenu>
#include <QStringList>
#include <QTest>
#include <QTranslator>

// The tray is the only surface a user sees while the window is hidden, so it
// has to carry the same state the window shows: the aggregate line, the
// all-roles toggle and one line per role.
class TrayMenuTest : public QObject
{
    Q_OBJECT

private slots:
    void menuCarriesAggregateStateAndOneLinePerRole();
    void toggleActionFollowsTheAggregateState();
    void russianTranslationAppliesToTheTrayMenu();

    // --- U3: the tray is part of the safety surface (docs/roadmap.md §10.1) ---
    void trayAuthorityLossNeverClaimsProtected();
    void trayConnectDisabledWhenMutationUnsafe();
    void trayDisconnectAvailableDuringAuthorityLoss();
    void trayDisabledBeforeAuthoritativeReconnectSnapshot();

private:
    static QStringList actionTexts(QMenu *menu)
    {
        QStringList texts;
        const QList<QAction *> actions = menu->actions();
        for (QAction *action : actions) {
            if (!action->isSeparator())
                texts << action->text();
        }
        return texts;
    }

    QAction *toggleAction(QMenu *menu) const
    {
        // The aggregate toggle carries the Disconnect/Connect-all label. It is
        // looked up by text, not by enabled state: the whole point of the
        // safety cases is to observe it becoming disabled.
        const QList<QAction *> actions = menu->actions();
        for (QAction *action : actions) {
            if (action->text().contains(QStringLiteral("Disconnect all"))
                || action->text().contains(QStringLiteral("Connect all")))
                return action;
        }
        return nullptr;
    }
};

void TrayMenuTest::menuCarriesAggregateStateAndOneLinePerRole()
{
    FakeCoreBackend core;
    DesktopIntegration desktop{&core};
    QMenu *menu = desktop.contextMenu();
    QVERIFY(menu);

    const QStringList texts = actionTexts(menu);
    // state line + toggle + window + quit + one line per role
    const int roleRows = core.roles()->rowCount();
    QVERIFY(roleRows > 0);
    QVERIFY2(texts.size() >= roleRows + 4,
             qPrintable(QStringLiteral("tray menu is missing lines: %1").arg(texts.join(QStringLiteral(" | ")))));

    // The first entry is informational: the aggregate state, never clickable.
    QVERIFY(!menu->actions().first()->isEnabled());

    for (int row = 0; row < roleRows; ++row) {
        const RoleSnapshot role = core.roles()->roleAt(row);
        const QString name = role.label.isEmpty() ? role.id : role.label;
        bool found = false;
        for (const QString &text : texts) {
            if (text.contains(name)) {
                found = true;
                break;
            }
        }
        QVERIFY2(found, qPrintable(QStringLiteral("no tray line for role %1 in [%2]")
                                       .arg(name, texts.join(QStringLiteral(" | ")))));
    }
}

void TrayMenuTest::toggleActionFollowsTheAggregateState()
{
    FakeCoreBackend core;
    DesktopIntegration desktop{&core};
    QMenu *menu = desktop.contextMenu();
    QVERIFY(menu);

    const QString before = core.aggregateActionText();
    core.connectAll();
    QTRY_VERIFY_WITH_TIMEOUT(core.aggregateActionText() != before, 5000);

    const QStringList texts = actionTexts(menu);
    const bool wantsDisconnect = core.aggregateActionText() == QStringLiteral("DISCONNECT");
    bool found = false;
    for (const QString &text : texts) {
        if (text.contains(wantsDisconnect ? QStringLiteral("Disconnect all")
                                          : QStringLiteral("Connect all")))
            found = true;
    }
    QVERIFY2(found, qPrintable(texts.join(QStringLiteral(" | "))));
}

void TrayMenuTest::russianTranslationAppliesToTheTrayMenu()
{
    QTranslator translator;
    if (!translator.load(QStringLiteral(":/i18n/kikimora_ru")))
        QSKIP("translations not compiled in (Qt LinguistTools unavailable)");
    QCoreApplication::installTranslator(&translator);
    FakeCoreBackend core;
    DesktopIntegration desktop{&core};
    const QStringList texts = actionTexts(desktop.contextMenu());
    QCoreApplication::removeTranslator(&translator);

    const QString expected = QStringLiteral("Показать Kikimora");
    bool found = false;
    for (const QString &text : texts) {
        if (text.contains(expected))
            found = true;
    }
    QVERIFY2(found, qPrintable(texts.join(QStringLiteral(" | "))));
}

void TrayMenuTest::trayAuthorityLossNeverClaimsProtected()
{
    FakeCoreBackend core;
    DesktopIntegration desktop{&core};
    QMenu *menu = desktop.contextMenu();
    QVERIFY(menu);

    core.connectAll();
    QTRY_COMPARE_WITH_TIMEOUT(core.aggregateState(), QStringLiteral("Ready"), 2000);
    const QStringList ready = actionTexts(menu);
    QVERIFY2(ready.join(QStringLiteral(" | ")).contains(QStringLiteral("Protected")),
             qPrintable(ready.join(QStringLiteral(" | "))));

    // Managed authority loss must flip the state line away from Protected.
    core.setManagedAuthority(true, false);
    core.setRouteAuthorityError(QStringLiteral("observer lost"));
    const QStringList lost = actionTexts(menu);
    const QString joined = lost.join(QStringLiteral(" | "));
    QVERIFY2(!joined.contains(QStringLiteral("Protected")), qPrintable(joined));
    QVERIFY2(joined.contains(QStringLiteral("Routing authority lost")), qPrintable(joined));
}

void TrayMenuTest::trayConnectDisabledWhenMutationUnsafe()
{
    FakeCoreBackend core;
    DesktopIntegration desktop{&core};
    QMenu *menu = desktop.contextMenu();
    QVERIFY(menu);

    // Stopped aggregate: the toggle offers CONNECT (a mutation). Authority
    // loss must disable it even though the tunnel roles still look fine.
    QCOMPARE(core.aggregateActionText(), QStringLiteral("CONNECT"));
    QAction *toggle = toggleAction(menu);
    QVERIFY(toggle);
    QVERIFY(toggle->isEnabled());
    core.setManagedAuthority(true, false);
    QTRY_VERIFY_WITH_TIMEOUT(!toggleAction(menu)->isEnabled(), 1000);
}

void TrayMenuTest::trayDisconnectAvailableDuringAuthorityLoss()
{
    FakeCoreBackend core;
    DesktopIntegration desktop{&core};
    QMenu *menu = desktop.contextMenu();
    QVERIFY(menu);

    core.connectAll();
    QTRY_COMPARE_WITH_TIMEOUT(core.aggregateState(), QStringLiteral("Ready"), 2000);
    core.setManagedAuthority(true, false);
    // Ready aggregate: the toggle offers DISCONNECT and stays enabled while
    // mutation is unsafe — disconnecting reduces exposure.
    QCOMPARE(core.aggregateActionText(), QStringLiteral("DISCONNECT"));
    QTRY_VERIFY_WITH_TIMEOUT(toggleAction(menu)->isEnabled(), 1000);
}

void TrayMenuTest::trayDisabledBeforeAuthoritativeReconnectSnapshot()
{
    FakeCoreBackend core;
    DesktopIntegration desktop{&core};
    QMenu *menu = desktop.contextMenu();
    QVERIFY(menu);

    // Without an authoritative startup-ready snapshot the channel is not
    // actionable for either action class.
    core.setStartupReady(false);
    QTRY_VERIFY_WITH_TIMEOUT(toggleAction(menu) == nullptr || !toggleAction(menu)->isEnabled(), 1000);
    core.setStartupReady(true);
    QTRY_VERIFY_WITH_TIMEOUT(toggleAction(menu) != nullptr && toggleAction(menu)->isEnabled(), 1000);
}

QTEST_MAIN(TrayMenuTest)
#include "TrayMenuTest.moc"
