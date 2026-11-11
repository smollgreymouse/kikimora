#include "DesktopIntegration.h"
#include "support/UiTestFixture.h"
#include "support/LiveCoreFixture.h"
#include "support/QmlWarningGuard.h"
#include "core/RealCoreClient.h"

#include <QApplication>
#include <QFileInfo>
#include <memory>
#include <utility>
#include <QQuickItem>
#include <QQuickWindow>
#include <QTest>

namespace {
QQuickItem *itemByName(QObject *root, const char *name)
{
    return root ? root->findChild<QQuickItem *>(QString::fromLatin1(name)) : nullptr;
}

QObject *objectByName(QObject *root, const char *name)
{
    return root ? root->findChild<QObject *>(QString::fromLatin1(name)) : nullptr;
}

QQuickItem *visualItemByName(QQuickItem *root, const char *name)
{
    if (!root) return nullptr;
    if (root->objectName() == QString::fromLatin1(name)) return root;
    for (QQuickItem *child : root->childItems()) {
        if (auto *match = visualItemByName(child, name)) return match;
    }
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

QPoint clickPoint(QQuickItem *item, QQuickWindow *window)
{
    return item->mapToItem(window->contentItem(), QPointF(item->width() / 2.0, item->height() / 2.0))
        .toPoint();
}

// Layout assigns geometry a frame after item creation; wait until the click
// point lands inside the window or the offscreen platform drops the event.
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
}

class RealCoreUiTest final : public QObject
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

private:
    // Layer C is mandatory (U5): every case owns its core through the
    // hermetic fixture, so missing binaries fail the case.
    bool startOwnedCore(QString *error, QString *socketPath)
    {
        QString coreBinary, toadBinary;
        if (!UiTest::LiveCoreFixture::resolveBinaries(&coreBinary, &toadBinary, error))
            return false;
        m_fixture = std::make_unique<UiTest::LiveCoreFixture>(coreBinary, toadBinary);
        if (!m_fixture->start(error))
            return false;
        *socketPath = m_fixture->socketPath();
        return true;
    }

    std::unique_ptr<UiTest::LiveCoreFixture> m_fixture;

private slots:
    void realCoreEventsReachQml()
    {
        QString error, socketPath;
        QVERIFY2(startOwnedCore(&error, &socketPath), qPrintable(error));

        // The shared production-equivalent bootstrap (U4): Basic style,
        // Core/Desktop/ThemeManager context, one engine per fixture.
        UiTest::UiTestFixture<RealCoreClient> fixture(socketPath);
        QVERIFY2(fixture.root, "Main.qml did not load");
        RealCoreClient &core = fixture.core;
        QObject *rootObject = fixture.root;
        QQuickWindow *window = fixture.window;
        QVERIFY(window);

        auto *coreStatus = itemByName(rootObject, "coreStatus");
        auto *circle = itemByName(rootObject, "connectCircle");
        auto *roleList = itemByName(rootObject, "roleList");
        QVERIFY(coreStatus);
        QVERIFY(circle);
        QVERIFY(roleList);

        QTRY_VERIFY_WITH_TIMEOUT(core.roles()->rowCount() >= 1, 3000);
        QTRY_VERIFY_WITH_TIMEOUT(roleList->property("count").toInt() >= 1, 3000);

        QCOMPARE(core.aggregateState(), QStringLiteral("Stopped"));
        QCOMPARE(circle->property("state").toString(), QStringLiteral("Stopped"));
        QCOMPARE(circle->property("actionText").toString(), QStringLiteral("CONNECT"));

        QVector<qulonglong> revisions;
        QVector<QString> aggregateStates;
        connect(&core, &CoreBackend::snapshotChanged, this, [&] {
            revisions.append(core.revision());
            aggregateStates.append(core.aggregateState());
        });

        QTest::mouseClick(window, Qt::LeftButton, Qt::NoModifier, clickPoint(circle, window));
        // Every role starts at once, so the aggregate passes Connecting and
        // PartiallyReady before it settles on Ready - give the real transports
        // time instead of assuming a warm, single-role start.
        QTRY_VERIFY_WITH_TIMEOUT(core.aggregateState() != QStringLiteral("Stopped"), 15000);
        QTRY_COMPARE_WITH_TIMEOUT(core.aggregateState(), QStringLiteral("Ready"), 90000);
        QTRY_COMPARE_WITH_TIMEOUT(circle->property("state").toString(), QStringLiteral("Ready"), 5000);
        QCOMPARE(circle->property("actionText").toString(), QStringLiteral("DISCONNECT"));
        // The current presentation states protection directly; the old
        // "Ready · r..." prefix no longer exists.
        QVERIFY2(coreStatus->property("text").toString().contains(QStringLiteral("Protected")),
                 qPrintable(coreStatus->property("text").toString()));
        QVERIFY(aggregateStates.contains(QStringLiteral("Connecting")) ||
                aggregateStates.contains(QStringLiteral("Ready")));

        const RoleSnapshot role = core.roles()->roleAt(0);
        QVERIFY(!role.protocol.isEmpty());
        QVERIFY(!role.interfaceName.isEmpty());
        QVERIFY(role.state == QStringLiteral("Ready") || role.state == QStringLiteral("Online"));
        QTRY_VERIFY_WITH_TIMEOUT(visualItemByName(window->contentItem(), "roleRow") != nullptr, 3000);

        const auto tabs = visualItemsByName(window->contentItem(), "bottomTabButton");
        // The current product is intentionally two pages: Home and Settings.
        QCOMPARE(tabs.size(), 2);
        auto *firstTab = qobject_cast<QQuickItem *>(tabs.at(0));
        QVERIFY(firstTab);
        for (int i = 1; i < tabs.size(); ++i) {
            auto *tab = qobject_cast<QQuickItem *>(tabs.at(i));
            QVERIFY(tab);
            QTest::mouseClick(window, Qt::LeftButton, Qt::NoModifier, clickPoint(tab, window));
            QTRY_COMPARE_WITH_TIMEOUT(rootObject->property("selectedTab").toInt(), i, 1000);
        }
        QTest::mouseClick(window, Qt::LeftButton, Qt::NoModifier, clickPoint(firstTab, window));
        QTRY_COMPARE_WITH_TIMEOUT(rootObject->property("selectedTab").toInt(), 0, 1000);

        QTest::mouseClick(window, Qt::LeftButton, Qt::NoModifier, clickPoint(circle, window));
        QTRY_COMPARE_WITH_TIMEOUT(core.aggregateState(), QStringLiteral("Stopped"), 60000);
        QTRY_COMPARE_WITH_TIMEOUT(circle->property("state").toString(), QStringLiteral("Stopped"), 3000);
        QCOMPARE(circle->property("actionText").toString(), QStringLiteral("CONNECT"));

        QTest::mouseClick(window, Qt::LeftButton, Qt::NoModifier, clickPoint(circle, window));
        // Every role starts at once, so the aggregate passes Connecting and
        // PartiallyReady before it settles on Ready - give the real transports
        // time instead of assuming a warm, single-role start.
        QTRY_VERIFY_WITH_TIMEOUT(core.aggregateState() != QStringLiteral("Stopped"), 15000);
        QTRY_COMPARE_WITH_TIMEOUT(core.aggregateState(), QStringLiteral("Ready"), 90000);
        QTRY_COMPARE_WITH_TIMEOUT(circle->property("state").toString(), QStringLiteral("Ready"), 5000);

        for (qsizetype i = 1; i < revisions.size(); ++i)
            QVERIFY2(revisions.at(i) > revisions.at(i - 1), "core revisions must be strictly increasing");
    }

    // The real local-mode core publishes its actual authority values: the
    // managed fields exist with their local-mode truth (roadmap §12.7).
    void realAuthorityFieldsReachQml()
    {
        QString error, socketPath;
        QVERIFY2(startOwnedCore(&error, &socketPath), qPrintable(error));
        UiTest::UiTestFixture<RealCoreClient> fixture(socketPath);
        QVERIFY2(fixture.root, "Main.qml did not load");
        RealCoreClient &core = fixture.core;

        QTRY_VERIFY_WITH_TIMEOUT(core.revision() > 0, 5000);
        QCOMPARE(core.apiCompatible(), true);
        QCOMPARE(core.startupReady(), true);
        // A local-mode core: not managed, no transition journal. The route
        // authority value is whatever this core really publishes — the case
        // pins that the backend maps it and the page renders it verbatim.
        QCOMPARE(core.managedMode(), false);
        QCOMPARE(core.modeTransition(), QStringLiteral("none"));

        auto *drawer = objectByName(fixture.root, "fullDiagnosticsDrawer");
        QVERIFY(drawer);
        QMetaObject::invokeMethod(drawer, "open");
        QTRY_VERIFY_WITH_TIMEOUT(drawer->property("opened").toBool(), 3000);
        auto *managed = itemByName(fixture.root, "diagManagedMode");
        auto *transition = itemByName(fixture.root, "diagModeTransition");
        auto *authority = itemByName(fixture.root, "diagRouteAuthority");
        auto *startup = itemByName(fixture.root, "diagStartupReady");
        QVERIFY(managed);
        QVERIFY(transition);
        QVERIFY(authority);
        QVERIFY(startup);
        QCOMPARE(managed->property("value").toString(), QStringLiteral("no"));
        QCOMPARE(transition->property("value").toString(), QStringLiteral("none"));
        QCOMPARE(startup->property("value").toString(), QStringLiteral("yes"));
        const QString expectedAuthority =
            core.routeAuthorityReady() ? QStringLiteral("ready") : QStringLiteral("not ready");
        QCOMPARE(authority->property("value").toString(), expectedAuthority);
    }

    void realFamilyReadinessReachesRoleDrawer()
    {
        QString error, socketPath;
        QVERIFY2(startOwnedCore(&error, &socketPath), qPrintable(error));
        UiTest::UiTestFixture<RealCoreClient> fixture(socketPath);
        QVERIFY2(fixture.root, "Main.qml did not load");
        RealCoreClient &core = fixture.core;
        QQuickWindow *window = fixture.window;
        QVERIFY(window);

        QTRY_VERIFY_WITH_TIMEOUT(core.roles()->rowCount() == 2, 5000);
        // The drawer must render the core-reported family fields verbatim.
        // A stopped local role has no configured families yet: both rows show
        // "не настроена" instead of falling back to a derived readiness.
        const RoleSnapshot stopped = core.roles()->roleAt(0);
        QCOMPARE(stopped.ipv4Configured, false);
        QCOMPARE(stopped.ipv6Configured, false);

        QTRY_VERIFY_WITH_TIMEOUT(visualItemByName(window->contentItem(), "roleRow") != nullptr, 5000);
        auto *row = visualItemByName(window->contentItem(), "roleRow");
        auto *drawer = objectByName(fixture.root, "roleDrawer");
        QTest::mouseClick(window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(row, window));
        QTRY_VERIFY_WITH_TIMEOUT(drawer->property("opened").toBool(), 3000);
        auto *diagToggle = itemByName(fixture.root, "roleDiagnosticsToggle");
        QVERIFY(diagToggle);
        QMetaObject::invokeMethod(diagToggle, "clicked");
        auto *ipv4 = itemByName(fixture.root, "roleIpv4Readiness");
        auto *ipv6 = itemByName(fixture.root, "roleIpv6Readiness");
        QVERIFY(ipv4);
        QVERIFY(ipv6);
        QCOMPARE(ipv4->property("value").toString(), QStringLiteral("не настроена"));
        QCOMPARE(ipv6->property("value").toString(), QStringLiteral("не настроена"));
        // Family readiness itself is managed-runtime truth (Layer D): a
        // local-mode core publishes no publication families even when the
        // transport is up, so this unconfigured rendering IS the full
        // Layer-C family contract.
    }

    void realSelectedRouteStatsReachDiagnostics()
    {
        QString error, socketPath;
        QVERIFY2(startOwnedCore(&error, &socketPath), qPrintable(error));
        UiTest::UiTestFixture<RealCoreClient> fixture(socketPath);
        QVERIFY2(fixture.root, "Main.qml did not load");
        RealCoreClient &core = fixture.core;
        QQuickWindow *window = fixture.window;

        QTRY_VERIFY_WITH_TIMEOUT(core.roles()->rowCount() == 2, 5000);
        // The counters row follows the core-published map: whatever this real
        // core reports (the local core publishes its zeroed counters), the
        // drawer renders exactly those fields (roadmap §12.7).
        const QVariantMap published = core.roles()->get(0).value(QStringLiteral("selectedRoutes")).toMap();
        QTRY_VERIFY_WITH_TIMEOUT(visualItemByName(window->contentItem(), "roleRow") != nullptr, 5000);
        auto *row = visualItemByName(window->contentItem(), "roleRow");
        auto *drawer = objectByName(fixture.root, "roleDrawer");
        QTest::mouseClick(window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(row, window));
        QTRY_VERIFY_WITH_TIMEOUT(drawer->property("opened").toBool(), 3000);
        auto *diagToggle = itemByName(fixture.root, "roleDiagnosticsToggle");
        QVERIFY(diagToggle);
        QMetaObject::invokeMethod(diagToggle, "clicked");
        auto *stats = itemByName(fixture.root, "roleSelectedRoutes");
        QVERIFY(stats);
        QCOMPARE(stats->isVisible(), !published.isEmpty());
        if (!published.isEmpty()) {
            const QString text = stats->property("value").toString();
            QVERIFY2(text.contains(QStringLiteral("registered")), qPrintable(text));
        }
    }
};

int main(int argc, char **argv)
{
    UiTest::ensureProductionBootstrap();
    QApplication app(argc, argv);
    app.setApplicationName(QStringLiteral("Kikimora"));
    app.setOrganizationName(QStringLiteral("Kikimora"));
    RealCoreUiTest test;
    return QTest::qExec(&test, argc, argv);
}

#include "RealCoreUiTest.moc"
