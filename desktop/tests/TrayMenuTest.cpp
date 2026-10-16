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

QTEST_MAIN(TrayMenuTest)
#include "TrayMenuTest.moc"
