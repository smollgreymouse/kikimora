// Live-core matrix: every control the window exposes must drive the core and
// reflect it back. Kept separate from RealCoreUiTest so each case can be read on
// its own and the fixture stays tiny.
#include "DesktopIntegration.h"
#include "support/UiTestFixture.h"
#include "support/LiveCoreFixture.h"
#include "support/QmlWarningGuard.h"
#include "core/RealCoreClient.h"
#include "models/RoleListModel.h"

#include <QAction>
#include <QApplication>
#include <QElapsedTimer>
#include <QJsonDocument>
#include <QJsonObject>
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

#include <memory>

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

/// The hermetic Layer-C fixture every matrix case owns (U5): missing binaries
/// are a harness failure, never a skip.
bool startOwnedCore(std::unique_ptr<UiTest::LiveCoreFixture> &fixture, QString *error, QString *socketPath)
{
    QString coreBinary, toadBinary;
    if (!UiTest::LiveCoreFixture::resolveBinaries(&coreBinary, &toadBinary, error))
        return false;
    fixture = std::make_unique<UiTest::LiveCoreFixture>(coreBinary, toadBinary);
    if (!fixture->start(error))
        return false;
    *socketPath = fixture->socketPath();
    return true;
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
    void initTestCase()
    {
        UiTest::QmlWarningGuard::instance().install();
    }

    void init()
    {
        UiTest::QmlWarningGuard::instance().reset();
    }

    void cleanup()
    {
        const QStringList unexpected = UiTest::QmlWarningGuard::instance().unexpectedMessages();
        QVERIFY2(unexpected.isEmpty(),
                 qPrintable(QStringLiteral("unexpected QML warnings:\n%1")
                                .arg(unexpected.join(QStringLiteral("\n")))));
    }

    void roleActionDrivesTheCore();
    void settingsShowLiveState();
    void profileSwitchDrivesTheCore();
    void createTunnelWritesConfiguration();
    void routingListsRoundTrip();
    void setRoleIpv6RoundTrip();
    void setBypassModeRoundTrip();
    void diagnosticsCaptureRoundTrip();
};

void RealCoreMatrixTest::roleActionDrivesTheCore()
{
    std::unique_ptr<UiTest::LiveCoreFixture> liveCore;
    QString error, socketPath;
    QVERIFY2(startOwnedCore(liveCore, &error, &socketPath), qPrintable(error));

    UiTest::UiTestFixture<RealCoreClient> fixture(socketPath);
    QVERIFY2(fixture.root, "Main.qml did not load");
    RealCoreClient &core = fixture.core;
    QObject *rootObject = fixture.root;
    QQuickWindow *window = fixture.window;
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
    QVERIFY2(opened, "the role row must open the drawer under the offscreen platform with the owned fixture");

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
    std::unique_ptr<UiTest::LiveCoreFixture> liveCore;
    QString error, socketPath;
    QVERIFY2(startOwnedCore(liveCore, &error, &socketPath), qPrintable(error));

    UiTest::UiTestFixture<RealCoreClient> fixture(socketPath);
    QVERIFY2(fixture.root, "Main.qml did not load");
    RealCoreClient &core = fixture.core;
    QObject *rootObject = fixture.root;
    QQuickWindow *window = fixture.window;
    QVERIFY(window);
    // coreState turns non-empty as soon as the socket connects; the snapshot fields
    // arrive with the first GetSnapshot/Subscribe answer, so wait for those.
    QTRY_VERIFY_WITH_TIMEOUT(!core.activeProfile().isEmpty(), 20000);
    QTRY_VERIFY_WITH_TIMEOUT(!core.underlaySummary().isEmpty(), 20000);

    const auto tabs = visualItemsByName(window->contentItem(), "bottomTabButton");
    // The current product is intentionally two pages: Home and Settings.
    QCOMPARE(tabs.size(), 2);
    auto *settingsTab = qobject_cast<QQuickItem *>(tabs.at(1));
    QVERIFY(settingsTab);
    QTest::mouseClick(window, Qt::LeftButton, Qt::NoModifier, clickPoint(settingsTab, window));
    QTRY_COMPARE_WITH_TIMEOUT(rootObject->property("selectedTab").toInt(), 1, 2000);

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
    std::unique_ptr<UiTest::LiveCoreFixture> liveCore;
    QString error, socketPath;
    QVERIFY2(startOwnedCore(liveCore, &error, &socketPath), qPrintable(error));

    UiTest::UiTestFixture<RealCoreClient> fixture(socketPath);
    QVERIFY2(fixture.root, "Main.qml did not load");
    RealCoreClient &core = fixture.core;
    QObject *rootObject = fixture.root;
    QQuickWindow *window = fixture.window;
    QVERIFY(window);
    // The alpha/beta fixture always carries default/home/work.
    QTRY_VERIFY_WITH_TIMEOUT(core.profiles().size() >= 3, 20000);

    const QString original = core.activeProfile();
    const QStringList profiles = core.profiles();
    QString target;
    for (const QString &profile : profiles)
        if (profile != original) { target = profile; break; }
    QVERIFY(!target.isEmpty());

    auto *combo = itemByName(rootObject, "profileCombo");
    QVERIFY2(combo, "profileCombo must exist on the home page");
    const QVariant model = combo->property("model");
    QStringList entries = model.toStringList();
    QVERIFY2(!entries.isEmpty(), "profile combo must expose the fixture profiles");
    const int index = entries.indexOf(target);
    QVERIFY2(index >= 0, qPrintable(QStringLiteral("fixture profile %1 missing from %2").arg(target, entries.join(QStringLiteral(",")))));

    // A user pick reaches the core through the combo's activated(int) signal;
    // assigning currentIndex alone only moves the visual selection.
    const bool picked = QMetaObject::invokeMethod(combo, "activated", Qt::DirectConnection,
                                                  Q_ARG(int, index));
    QVERIFY2(picked, "profileCombo must expose activated(int)");
    QTRY_COMPARE_WITH_TIMEOUT(core.activeProfile(), target, 20000);

    QMetaObject::invokeMethod(combo, "activated", Qt::DirectConnection,
                              Q_ARG(int, entries.indexOf(original)));
    QTRY_COMPARE_WITH_TIMEOUT(core.activeProfile(), original, 20000);
}

void RealCoreMatrixTest::routingListsRoundTrip()
{
    std::unique_ptr<UiTest::LiveCoreFixture> liveCore;
    QString error, socketPath;
    QVERIFY2(startOwnedCore(liveCore, &error, &socketPath), qPrintable(error));

    RealCoreClient core(socketPath, nullptr);
    QTRY_VERIFY_WITH_TIMEOUT(core.roles()->rowCount() >= 1, 20000);

    core.refreshRoutingLists();
    QTRY_VERIFY_WITH_TIMEOUT(!core.routingLists().isEmpty(), 15000);

    // The fixture owns exact alpha/beta lists (roadmap §12.6): the test is
    // hermetic to its own data, not to a developer host naming (P1-UI9).
    const QVariantMap before = core.routingLists();
    QVERIFY2(before.contains(QStringLiteral("alpha")),
             qPrintable(QStringLiteral("fixture lists missing: %1").arg(before.keys().join(QStringLiteral(",")))));
    const QString listName = QStringLiteral("alpha");
    const QString original = before.value(listName).toString();
    const QString marker = QStringLiteral("kikimora-matrix-test.example");
    const QString payload = original + marker + QStringLiteral("\n");

    core.saveRoutingList(listName, payload);
    QTRY_VERIFY_WITH_TIMEOUT(core.lastCommandMessage().contains(QStringLiteral("saved")), 30000);
    QVERIFY2(!core.lastCommandMessage().contains(QStringLiteral("did not restart")),
             qPrintable(core.lastCommandMessage()));

    core.refreshRoutingLists();
    QTRY_VERIFY_WITH_TIMEOUT(
        core.routingLists().value(listName).toString().contains(marker), 15000);

    // Put the operator's list back exactly as it was.
    core.saveRoutingList(listName, original);
    QTRY_VERIFY_WITH_TIMEOUT(core.lastCommandMessage().contains(QStringLiteral("saved")), 30000);
    core.refreshRoutingLists();
    // A save normalises the file (blank lines go, entries are lowered), so compare meaning:
    // the test marker is gone and every comment line survived.
    QTRY_VERIFY_WITH_TIMEOUT(
        !core.routingLists().value(listName).toString().contains(marker), 15000);
    QCOMPARE(core.routingLists().value(listName).toString().count(QLatin1Char('#')),
             original.count(QLatin1Char('#')));
}

void RealCoreMatrixTest::createTunnelWritesConfiguration()
{
    std::unique_ptr<UiTest::LiveCoreFixture> liveCore;
    QString error, socketPath;
    QVERIFY2(startOwnedCore(liveCore, &error, &socketPath), qPrintable(error));

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

void RealCoreMatrixTest::setRoleIpv6RoundTrip()
{
    std::unique_ptr<UiTest::LiveCoreFixture> liveCore;
    QString error, socketPath;
    QVERIFY2(startOwnedCore(liveCore, &error, &socketPath), qPrintable(error));
    RealCoreClient core(socketPath, nullptr);
    QTRY_VERIFY_WITH_TIMEOUT(core.roles()->rowCount() == 2, 20000);

    // The fixture role starts with the AAAA policy on: flip it off and back
    // through the same command the drawer switch drives.
    const auto ipv6Dump = [&core]() {
        QString states;
        for (int i = 0; i < core.roles()->rowCount(); ++i) {
            const QVariantMap r = core.roles()->get(i);
            states += QStringLiteral("%1 ipv6=%2 route=%3; ")
                          .arg(r.value(QStringLiteral("roleId")).toString())
                          .arg(r.value(QStringLiteral("leshyIpv6")).toBool())
                          .arg(r.value(QStringLiteral("routeReady")).toBool());
        }
        return QStringLiteral("rows=%1 error='%2' %3")
            .arg(core.roles()->rowCount())
            .arg(core.lastCommandError(), states);
    };
    QTRY_COMPARE_WITH_TIMEOUT(core.roles()->rowCount(), 2, 20000);
    QElapsedTimer ipv6Timer;
    ipv6Timer.start();
    while (!core.roles()->roleAt(0).leshyIpv6 && ipv6Timer.elapsed() < 10000)
        QTest::qWait(100);
    if (!core.roles()->roleAt(0).leshyIpv6)
        QFAIL(qPrintable(QStringLiteral("initial leshyIpv6 must be true: %1").arg(ipv6Dump())));
    core.setRoleIpv6(0, false);
    ipv6Timer.restart();
    while (core.roles()->roleAt(0).leshyIpv6 && ipv6Timer.elapsed() < 20000)
        QTest::qWait(100);
    if (core.roles()->roleAt(0).leshyIpv6)
        QFAIL(qPrintable(QStringLiteral("SetRoleIpv6(false) did not apply: %1").arg(ipv6Dump())));
    const qulonglong revisionBeforeTrue = core.revision();
    core.setRoleIpv6(0, true);
    ipv6Timer.restart();
    while (!core.roles()->roleAt(0).leshyIpv6 && ipv6Timer.elapsed() < 20000)
        QTest::qWait(100);
    if (!core.roles()->roleAt(0).leshyIpv6)
        QFAIL(qPrintable(QStringLiteral("SetRoleIpv6(true) did not apply: %1 rev %2->%3")
                             .arg(ipv6Dump())
                             .arg(revisionBeforeTrue)
                             .arg(core.revision())));
}

void RealCoreMatrixTest::setBypassModeRoundTrip()
{
    std::unique_ptr<UiTest::LiveCoreFixture> liveCore;
    QString error, socketPath;
    QVERIFY2(startOwnedCore(liveCore, &error, &socketPath), qPrintable(error));
    RealCoreClient core(socketPath, nullptr);
    QTRY_VERIFY_WITH_TIMEOUT(core.roles()->rowCount() == 2, 20000);

    // Default is direct; a role-backed rule round-trips and comes back.
    QTRY_COMPARE_WITH_TIMEOUT(core.bypassMode(), QStringLiteral("direct"), 20000);
    core.setBypassMode(QStringLiteral("alpha"));
    QTRY_COMPARE_WITH_TIMEOUT(core.bypassMode(), QStringLiteral("alpha"), 20000);
    core.setBypassMode(QStringLiteral("direct"));
    QTRY_COMPARE_WITH_TIMEOUT(core.bypassMode(), QStringLiteral("direct"), 20000);
}

void RealCoreMatrixTest::diagnosticsCaptureRoundTrip()
{
    std::unique_ptr<UiTest::LiveCoreFixture> liveCore;
    QString error, socketPath;
    QVERIFY2(startOwnedCore(liveCore, &error, &socketPath), qPrintable(error));
    RealCoreClient core(socketPath, nullptr);
    QTRY_VERIFY_WITH_TIMEOUT(core.roles()->rowCount() == 2, 20000);

    // The core runs with --diagnostics-host-sources=false: the capture works
    // end to end while reading no ambient host logs (the fixture spool lives
    // under <root>/state/diagnostics).
    core.setDiagnosticsCapture(true);
    QTRY_VERIFY_WITH_TIMEOUT(core.diagnostics().contains(QStringLiteral("capture")), 20000);
    QVariantMap capture = core.diagnostics().value(QStringLiteral("capture")).toMap();
    QTRY_COMPARE_WITH_TIMEOUT(capture.value(QStringLiteral("enabled")).toBool(), true, 20000);

    core.setDiagnosticsCapture(false);
    QTRY_VERIFY_WITH_TIMEOUT(
        !core.diagnostics().value(QStringLiteral("capture")).toMap().value(QStringLiteral("enabled")).toBool(),
        20000);
}

int main(int argc, char **argv)
{
    UiTest::ensureProductionBootstrap();
    QApplication app(argc, argv);
    app.setApplicationName(QStringLiteral("Kikimora"));
    app.setOrganizationName(QStringLiteral("Kikimora"));
    RealCoreMatrixTest test;
    return QTest::qExec(&test, argc, argv);
}

#include "RealCoreMatrixTest.moc"
