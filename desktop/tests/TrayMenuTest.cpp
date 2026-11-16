#include "DesktopIntegration.h"

#include "core/FakeCoreBackend.h"
#include "models/RoleListModel.h"

#include <QAction>
#include <QCoreApplication>
#include <QClipboard>
#include <QDir>
#include <QGuiApplication>
#include <QImage>
#include <QPainter>
#include <QPixmap>
#include <QWindow>
#include <QFile>
#include <QMenu>
#include <QSettings>
#include <QStandardPaths>
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

    // --- U11: the autostart contract behind the tray toggle (roadmap §18.4) ---
    void autostartEntryIsCreatedAndRemoved();
    void windowAndClipboardHelpers();
    void trayIconFallbackCropsTransparentPadding();

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

// U11: setAutostartEnabled writes an XDG autostart entry on Linux and removes it
// again. The path comes from QStandardPaths::GenericConfigLocation, so the test
// redirects XDG_CONFIG_HOME to a temporary directory and never touches the real
// user profile.
void TrayMenuTest::autostartEntryIsCreatedAndRemoved()
{
#ifdef Q_OS_WIN
    // Windows has no XDG desktop file: autostart is the per-user Run entry, or
    // the installer's all-users Startup shortcut when that exists. Assert the
    // native contract and restore the runner's previous value afterwards.
    const QString shortcut = QStringLiteral(
        "C:\\ProgramData\\Microsoft\\Windows\\Start Menu\\Programs\\StartUp\\Kikimora.lnk");
    const bool installerOwned = QFile::exists(shortcut);
    QSettings run(QStringLiteral("HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Run"),
                  QSettings::NativeFormat);
    const QVariant previous = run.value(QStringLiteral("Kikimora"));
    run.remove(QStringLiteral("Kikimora"));
    run.sync();

    FakeCoreBackend core;
    DesktopIntegration desktop{&core};
    QVERIFY2(installerOwned || !desktop.autostartEnabled(),
             "a clean Run key without the installer shortcut must report autostart off");

    desktop.setAutostartEnabled(true);
    run.sync();
    QVERIFY2(run.contains(QStringLiteral("Kikimora")), "autostart did not write the per-user Run entry");
    const QString target = desktop.autostartTarget();
    QVERIFY2(target.contains(QStringLiteral("--background")),
             qPrintable(QStringLiteral("Run entry does not launch the UI in the background: %1").arg(target)));
    QVERIFY2(desktop.autostartEnabled(), "autostart must report enabled once the Run entry exists");

    desktop.setAutostartEnabled(false);
    run.sync();
    if (!installerOwned) {
        QVERIFY2(!run.contains(QStringLiteral("Kikimora")), "disabling autostart left the Run entry behind");
        QVERIFY2(!desktop.autostartEnabled(), "autostart still reports enabled after disabling");
    }

    if (previous.isValid())
        run.setValue(QStringLiteral("Kikimora"), previous);
    else
        run.remove(QStringLiteral("Kikimora"));
    run.sync();
#else
    const QString sandbox = QDir::tempPath() +
                            QStringLiteral("/kikimora-autostart-%1")
                                .arg(QCoreApplication::applicationPid());
    QDir().mkpath(sandbox);

    // QStandardPaths caches per process, so the override must be installed before
    // any lookup and cannot be undone reliably: restore it in the same block.
    const QByteArray previous = qgetenv("XDG_CONFIG_HOME");
    qputenv("XDG_CONFIG_HOME", sandbox.toUtf8());
    QStandardPaths::setTestModeEnabled(false);

    FakeCoreBackend core;
    DesktopIntegration desktop{&core};

    const QString target = desktop.autostartTarget();
    QVERIFY2(!desktop.autostartEnabled(), "a fresh sandbox must not report autostart enabled");

    desktop.setAutostartEnabled(true);
    QVERIFY2(QFile::exists(target), qPrintable(QStringLiteral("autostart entry missing: %1").arg(target)));
    QVERIFY2(desktop.autostartEnabled(), "autostart must report enabled once the entry exists");

    // The entry must be a parseable desktop file that launches the application in
    // the background; a silent wrong Exec line would only show up at next login.
    QFile file(target);
    QVERIFY(file.open(QIODevice::ReadOnly));
    const QString content = QString::fromUtf8(file.readAll());
    file.close();
    QVERIFY2(content.contains(QStringLiteral("[Desktop Entry]")),
             "autostart entry is not a desktop file");
    QVERIFY2(content.contains(QStringLiteral("Exec=")), "autostart entry has no Exec line");
    QVERIFY2(content.contains(QStringLiteral("--background")),
             "autostart entry does not launch the UI in the background");

    desktop.setAutostartEnabled(false);
    QVERIFY2(!QFile::exists(target), "disabling autostart left the entry behind");
    QVERIFY2(!desktop.autostartEnabled(), "autostart still reports enabled after disabling");

    QDir(sandbox).removeRecursively();
    if (previous.isEmpty())
        qunsetenv("XDG_CONFIG_HOME");
    else
        qputenv("XDG_CONFIG_HOME", previous);
#endif
}

void TrayMenuTest::windowAndClipboardHelpers()
{
    FakeCoreBackend core;
    DesktopIntegration desktop{&core};
    QWindow window;
    window.show();
    desktop.setWindow(&window);
    desktop.showWindow();
    QVERIFY(window.isVisible());
    desktop.hideWindow();
    QVERIFY(!window.isVisible());
    QGuiApplication::clipboard()->setText(QStringLiteral("kikimora-paste"));
    QCOMPARE(desktop.clipboardText(), QStringLiteral("kikimora-paste"));
    desktop.setWindow(nullptr);
    desktop.showWindow();
    desktop.hideWindow();
}

void TrayMenuTest::trayIconFallbackCropsTransparentPadding()
{
    QImage image(64, 64, QImage::Format_ARGB32);
    image.fill(Qt::transparent);
    QPainter painter(&image);
    painter.fillRect(QRect(16, 16, 32, 32), Qt::red);
    painter.end();

    const QIcon icon = DesktopIntegration::trayIconForImage(image);
    QVERIFY(!icon.isNull());
    const QPixmap pixmap = icon.pixmap(64, 64);
    QVERIFY(pixmap.width() < 64);
    QVERIFY(pixmap.width() >= 32);
    QVERIFY(DesktopIntegration::trayIconForImage(QImage()).isNull());
}

QTEST_MAIN(TrayMenuTest)
#include "TrayMenuTest.moc"
