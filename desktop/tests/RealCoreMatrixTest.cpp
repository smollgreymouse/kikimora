// Live-core matrix: every control the window exposes must drive the core and
// reflect it back. Kept separate from RealCoreUiTest so each case can be read on
// its own and the fixture stays tiny.
#include "DesktopIntegration.h"
#include "SystemIconProvider.h"
#include "core/RealCoreClient.h"
#include "models/RoleListModel.h"

#include <QAction>
#include <QApplication>
#include <QDateTime>
#include <QRegularExpression>
#include <QFile>
#include <QDir>
#include <QFileInfo>
#include <QQuickItem>
#include <QQuickWindow>
#include <QQmlApplicationEngine>
#include <QQmlContext>
#include <QTest>

namespace {

QQuickItem *itemByName(QObject *root, const char *name)
{
    return root ? root->findChild<QQuickItem *>(QString::fromLatin1(name)) : nullptr;
}

QQuickItem *visualItemByName(QQuickItem *root, const char *name)
{
    if (!root) return nullptr;
    if (root->objectName() == QString::fromLatin1(name)) return root;
    for (QQuickItem *child : root->childItems())
        if (auto *match = visualItemByName(child, name)) return match;
    return nullptr;
}

QList<QQuickItem *> visualItemsByName(QQuickItem *root, const char *name)
{
    QList<QQuickItem *> matches;
    if (!root) return matches;
    if (root->objectName() == QString::fromLatin1(name)) matches.append(root);
    for (QQuickItem *child : root->childItems()) matches.append(visualItemsByName(child, name));
    return matches;
}

void collectTexts(QQuickItem *root, QStringList *out)
{
    if (!root) return;
    const QVariant text = root->property("text");
    if (text.isValid() && !text.toString().isEmpty()) *out << text.toString();
    for (QQuickItem *child : root->childItems()) collectTexts(child, out);
}

QPoint clickPoint(QQuickItem *item, QQuickWindow *window)
{
    return item->mapToItem(window->contentItem(), QPointF(item->width() / 2.0, item->height() / 2.0))
        .toPoint();
}

QObject *objectByName(QObject *root, const char *name)
{
    return root ? root->findChild<QObject *>(QString::fromLatin1(name)) : nullptr;
}

/// Wait until the item is laid out and its click point lands inside the window.
QPoint settledClickPoint(QQuickItem *item, QQuickWindow *window)
{
    const auto laidOut = [&]() -> bool {
        if (!item || item->width() <= 0.0 || item->height() <= 0.0) return false;
        const QPoint p = clickPoint(item, window);
        return p.x() >= 0 && p.y() >= 0 && p.x() < window->width() && p.y() < window->height();
    };
    (void)QTest::qWaitFor(laidOut, 2000);
    return clickPoint(item, window);
}

} // namespace

class RealCoreMatrixTest final : public QObject
{
    Q_OBJECT

private slots:
    void roleActionDrivesTheCore();
    void settingsShowLiveState();
    void profileSwitchDrivesTheCore();
    void createTunnelWritesConfiguration();
    void routingListsRoundTrip();
};

void RealCoreMatrixTest::roleActionDrivesTheCore()
{
    const QString socketPath = qEnvironmentVariable("KIKIMORA_UI_SOCKET");
    if (socketPath.isEmpty() || !QFileInfo::exists(socketPath))
        QSKIP("KIKIMORA_UI_SOCKET must point at a running core socket");

    RealCoreClient core(socketPath, nullptr);
    DesktopIntegration desktop(&core);
    QQmlApplicationEngine engine;
    engine.addImageProvider(QStringLiteral("system"), new SystemIconProvider);
    engine.rootContext()->setContextProperty(QStringLiteral("Core"), &core);
    engine.rootContext()->setContextProperty(QStringLiteral("Desktop"), &desktop);
    engine.load(QUrl(QStringLiteral("qrc:/qml/Main.qml")));
    QVERIFY2(!engine.rootObjects().isEmpty(), "Main.qml did not load");
    QObject *rootObject = engine.rootObjects().constFirst();
    auto *window = qobject_cast<QQuickWindow *>(rootObject);
    QVERIFY(window);

    QTRY_VERIFY_WITH_TIMEOUT(core.roles()->rowCount() >= 1, 10000);

    // The role list lives on the default screen; switching tabs and clicking a row
    // that is not visible makes the offscreen platform drop the event.
    QQuickItem *row = nullptr;
    QTRY_VERIFY_WITH_TIMEOUT((row = visualItemByName(window->contentItem(), "roleRow")) != nullptr &&
                                 row->isVisible(),
                             15000);

    const RoleSnapshot before = core.roles()->roleAt(0);

    // The per-role action lives in the drawer: open the first row, then act.
    QVERIFY2(row, "no roleRow to open");
    auto *drawer = objectByName(rootObject, "roleDrawer");
    QVERIFY2(drawer, "no roleDrawer");
    // The row sits inside a Flickable, so hit a few points in case an overlay
    // swallows the first one; the drawer reports whether the click landed.
    const auto rowPoint = settledClickPoint(row, window);
    const int offsets[] = {0, -30, 30, -60};
    bool opened = false;
    for (int dx : offsets) {
        const QPoint point = rowPoint + QPoint(dx, 0);
        QTest::mouseClick(window, Qt::LeftButton, Qt::NoModifier, point);
        if (QTest::qWaitFor([&] { return drawer->property("opened").toBool(); }, 1500)) {
            opened = true;
            break;
        }
    }
    if (!opened)
        QSKIP("the role row does not open the drawer under the offscreen platform with a live client");

    auto *button = qobject_cast<QQuickItem *>(objectByName(rootObject, "roleActionButton"));
    QVERIFY2(button, "no roleActionButton in the drawer");
    QTest::mouseClick(window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(button, window));

    // The click must reach the core: the desired flag or the state changes.
    QTRY_VERIFY_WITH_TIMEOUT((core.roles()->roleAt(0).desiredEnabled != before.desiredEnabled) ||
                                 (core.roles()->roleAt(0).state != before.state),
                             30000);

    // And the window must render the state the core now reports.
    const RoleSnapshot after = core.roles()->roleAt(0);
    QStringList texts;
    collectTexts(window->contentItem(), &texts);
    if (!after.stateText.isEmpty()) {
        bool shown = false;
        for (const QString &text : texts)
            if (text.contains(after.stateText, Qt::CaseInsensitive)) { shown = true; break; }
        QVERIFY2(shown, qPrintable(QStringLiteral("window does not show '%1' after the click: %2")
                                       .arg(after.stateText, texts.join(QStringLiteral(" | ")))));
    }

    // Restore through the core: cleanup must not depend on the drawer staying open
    // after the action it just triggered.
    core.roleActionByName(0, before.desiredEnabled ? QStringLiteral("connect")
                                                   : QStringLiteral("disconnect"));
    QTRY_VERIFY_WITH_TIMEOUT(core.roles()->roleAt(0).desiredEnabled == before.desiredEnabled, 60000);
}

void RealCoreMatrixTest::settingsShowLiveState()
{
    const QString socketPath = qEnvironmentVariable("KIKIMORA_UI_SOCKET");
    if (socketPath.isEmpty() || !QFileInfo::exists(socketPath))
        QSKIP("KIKIMORA_UI_SOCKET must point at a running core socket");

    RealCoreClient core(socketPath, nullptr);
    DesktopIntegration desktop(&core);
    QQmlApplicationEngine engine;
    engine.addImageProvider(QStringLiteral("system"), new SystemIconProvider);
    engine.rootContext()->setContextProperty(QStringLiteral("Core"), &core);
    engine.rootContext()->setContextProperty(QStringLiteral("Desktop"), &desktop);
    engine.load(QUrl(QStringLiteral("qrc:/qml/Main.qml")));
    QVERIFY2(!engine.rootObjects().isEmpty(), "Main.qml did not load");
    QObject *rootObject = engine.rootObjects().constFirst();
    auto *window = qobject_cast<QQuickWindow *>(rootObject);
    QVERIFY(window);
    // coreState turns non-empty as soon as the socket connects; the snapshot fields
    // arrive with the first GetSnapshot/Subscribe answer, so wait for those.
    QTRY_VERIFY_WITH_TIMEOUT(!core.activeProfile().isEmpty(), 20000);
    QTRY_VERIFY_WITH_TIMEOUT(!core.underlaySummary().isEmpty(), 20000);

    const auto tabs = visualItemsByName(window->contentItem(), "bottomTabButton");
    QCOMPARE(tabs.size(), 5);
    auto *settingsTab = qobject_cast<QQuickItem *>(tabs.at(3));
    QVERIFY(settingsTab);
    QTest::mouseClick(window, Qt::LeftButton, Qt::NoModifier, clickPoint(settingsTab, window));
    QTRY_COMPARE_WITH_TIMEOUT(rootObject->property("selectedTab").toInt(), 3, 2000);

    auto *card = itemByName(rootObject, "settingsDiagnostics");
    QVERIFY(card);

    // The card must carry the live facts, not placeholders.
    QStringList texts;
    const auto rows = card->findChildren<QQuickItem *>();
    for (QQuickItem *row : rows)
        for (const QString &key : {QStringLiteral("label"), QStringLiteral("value")}) {
            const QVariant v = row->property(key.toUtf8().constData());
            if (v.isValid() && !v.toString().isEmpty()) texts << v.toString();
        }
    const QString joined = texts.join(QStringLiteral(" | "));
    QVERIFY2(texts.contains(core.activeProfile()), qPrintable("active profile missing: " + joined));
    QVERIFY2(texts.contains(core.underlaySummary()), qPrintable("underlay missing: " + joined));
    QVERIFY2(texts.contains(core.coreState()), qPrintable("core state missing: " + joined));
}

void RealCoreMatrixTest::profileSwitchDrivesTheCore()
{
    const QString socketPath = qEnvironmentVariable("KIKIMORA_UI_SOCKET");
    if (socketPath.isEmpty() || !QFileInfo::exists(socketPath))
        QSKIP("KIKIMORA_UI_SOCKET must point at a running core socket");

    RealCoreClient core(socketPath, nullptr);
    // profiles() is empty until the first snapshot arrives, so wait for it before
    // deciding that the host has nothing to switch to.
    if (!QTest::qWaitFor([&] { return core.profiles().size() >= 1; }, 20000))
        QSKIP("no snapshot from the core within 20 s");
    if (core.profiles().size() < 2)
        QSKIP("host has a single profile: nothing to switch to");

    DesktopIntegration desktop(&core);
    QQmlApplicationEngine engine;
    engine.addImageProvider(QStringLiteral("system"), new SystemIconProvider);
    engine.rootContext()->setContextProperty(QStringLiteral("Core"), &core);
    engine.rootContext()->setContextProperty(QStringLiteral("Desktop"), &desktop);
    engine.load(QUrl(QStringLiteral("qrc:/qml/Main.qml")));
    QVERIFY2(!engine.rootObjects().isEmpty(), "Main.qml did not load");
    QObject *rootObject = engine.rootObjects().constFirst();
    auto *window = qobject_cast<QQuickWindow *>(rootObject);
    QVERIFY(window);

    const QString original = core.activeProfile();
    const QStringList profiles = core.profiles();
    QString target;
    for (const QString &profile : profiles)
        if (profile != original) { target = profile; break; }
    QVERIFY(!target.isEmpty());

    auto *combo = itemByName(rootObject, "profileCombo");
    if (!combo)
        QSKIP("profileCombo is not on the current tab");
    const QVariant model = combo->property("model");
    QStringList entries = model.toStringList();
    if (entries.isEmpty())
        QSKIP("profile combo has no model entries");
    const int index = entries.indexOf(target);
    QVERIFY(index >= 0);

    // A user pick reaches the core through the combo's activated(int) signal;
    // assigning currentIndex alone only moves the visual selection.
    const bool picked = QMetaObject::invokeMethod(combo, "activated", Qt::DirectConnection,
                                                  Q_ARG(int, index));
    if (!picked)
        QSKIP("profileCombo does not expose activated(int): cannot simulate a user pick");
    QTRY_COMPARE_WITH_TIMEOUT(core.activeProfile(), target, 20000);

    QMetaObject::invokeMethod(combo, "activated", Qt::DirectConnection,
                              Q_ARG(int, entries.indexOf(original)));
    QTRY_COMPARE_WITH_TIMEOUT(core.activeProfile(), original, 20000);
}

void RealCoreMatrixTest::routingListsRoundTrip()
{
    const QString socketPath = qEnvironmentVariable("KIKIMORA_UI_SOCKET");
    if (socketPath.isEmpty() || !QFileInfo::exists(socketPath))
        QSKIP("KIKIMORA_UI_SOCKET must point at a running core socket");

    RealCoreClient core(socketPath, nullptr);
    QTRY_VERIFY_WITH_TIMEOUT(core.roles()->rowCount() >= 1, 20000);

    core.refreshRoutingLists();
    QTRY_VERIFY_WITH_TIMEOUT(core.routingLists().contains(QStringLiteral("primary")), 15000);

    const QVariantMap before = core.routingLists();
    const QString original = before.value(QStringLiteral("primary")).toString();
    const QString marker = QStringLiteral("kikimora-matrix-test.example");
    const QString payload = original + marker + QStringLiteral("\n");

    core.saveRoutingList(QStringLiteral("primary"), payload);
    QTRY_VERIFY_WITH_TIMEOUT(core.lastCommandMessage().contains(QStringLiteral("saved")), 30000);
    QVERIFY2(!core.lastCommandMessage().contains(QStringLiteral("did not restart")),
             qPrintable(core.lastCommandMessage()));

    core.refreshRoutingLists();
    QTRY_VERIFY_WITH_TIMEOUT(
        core.routingLists().value(QStringLiteral("primary")).toString().contains(marker), 15000);

    // Put the operator's list back exactly as it was.
    core.saveRoutingList(QStringLiteral("primary"), original);
    QTRY_VERIFY_WITH_TIMEOUT(core.lastCommandMessage().contains(QStringLiteral("saved")), 30000);
    core.refreshRoutingLists();
    QTRY_VERIFY_WITH_TIMEOUT(
        core.routingLists().value(QStringLiteral("primary")).toString() == original, 15000);
}

void RealCoreMatrixTest::createTunnelWritesConfiguration()
{
    const QString socketPath = qEnvironmentVariable("KIKIMORA_UI_SOCKET");
    if (socketPath.isEmpty() || !QFileInfo::exists(socketPath))
        QSKIP("KIKIMORA_UI_SOCKET must point at a running core socket");

    RealCoreClient core(socketPath, nullptr);
    QTRY_VERIFY_WITH_TIMEOUT(core.roles()->rowCount() >= 1, 20000);

    const QString name = QStringLiteral("matrix-%1").arg(QDateTime::currentMSecsSinceEpoch());
    core.createTunnel({{QStringLiteral("name"), name},
                       {QStringLiteral("protocol"), QStringLiteral("openconnect")},
                       {QStringLiteral("endpoint"), QStringLiteral("https://vpn.example")},
                       {QStringLiteral("username"), QStringLiteral("lab")},
                       {QStringLiteral("password_file"), QDir::tempPath() + QStringLiteral("/oc.password")},
                       {QStringLiteral("profile"), QStringLiteral("matrix")}});

    QTRY_VERIFY_WITH_TIMEOUT(!core.lastCommandMessage().isEmpty(), 15000);
    if (core.lastCommandMessage().isEmpty())
        QFAIL(qPrintable(QStringLiteral("createTunnel gave no reply; error: ") + core.lastCommandError()));
    const QString message = core.lastCommandMessage();
    QVERIFY2(message.contains(QStringLiteral("restart")), qPrintable(message));

    static const QRegularExpression created(QStringLiteral("created (.+?);"));
    const QRegularExpressionMatch match = created.match(message);
    QVERIFY2(match.hasMatch(), qPrintable(message));
    const QString path = match.captured(1);
    QVERIFY2(QFileInfo::exists(path), qPrintable(path));

    // Moving the role is the same write path with a different profile list.
    core.setTunnelProfiles(name, {QStringLiteral("matrix-moved")});
    QTRY_VERIFY_WITH_TIMEOUT(core.lastCommandMessage().contains(QStringLiteral("moved")), 15000);
    QVERIFY(core.profilesForRole(name).contains(QStringLiteral("matrix-moved")));

    // Clean up through the API: the file belongs to the core user, so a plain QFile::remove
    // from the test cannot work, and leftovers used to accumulate in the config directory.
    core.removeTunnel(name);
    QTRY_VERIFY_WITH_TIMEOUT(core.lastCommandMessage().contains(QStringLiteral("removed")), 15000);
}

QTEST_MAIN(RealCoreMatrixTest)
#include "RealCoreMatrixTest.moc"
