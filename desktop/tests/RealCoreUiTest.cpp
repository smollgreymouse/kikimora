#include "DesktopIntegration.h"
#include "SystemIconProvider.h"
#include "core/RealCoreClient.h"

#include <QApplication>
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
}

class RealCoreUiTest final : public QObject
{
    Q_OBJECT

private slots:
    void realCoreEventsReachQml()
    {
        // Runs only where a live core exists (the lab VMs); everywhere else the
        // case skips so the suite stays green in CI.
        const QString socketPath = qEnvironmentVariable("KIKIMORA_UI_SOCKET");
        if (socketPath.isEmpty())
            QSKIP("KIKIMORA_UI_SOCKET is not set: no live core to drive");
        if (!QFileInfo::exists(socketPath))
            QSKIP("KIKIMORA_UI_SOCKET points at a missing socket");

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

        auto *coreStatus = itemByName(rootObject, "coreStatus");
        auto *circle = itemByName(rootObject, "connectCircle");
        auto *roleList = itemByName(rootObject, "roleList");
        QVERIFY(coreStatus);
        QVERIFY(circle);
        QVERIFY(roleList);

        QTRY_VERIFY_WITH_TIMEOUT(core.roles()->rowCount() >= 1, 3000);
        QTRY_VERIFY_WITH_TIMEOUT(roleList->property("count").toInt() >= 1, 3000);

        const QString mode = qEnvironmentVariable("KIKIMORA_UI_TEST_MODE", "lifecycle");
        if (mode == QStringLiteral("observe-not-ready")) {
            // The real backend intentionally waits for a stale transport
            // health window before declaring an outage.
            QTRY_VERIFY_WITH_TIMEOUT(core.aggregateState() != QStringLiteral("Ready"), 60000);
            QTRY_COMPARE_WITH_TIMEOUT(circle->property("state").toString(), core.aggregateState(), 3000);
            QVERIFY(!coreStatus->property("text").toString().isEmpty());
            return;
        }
        if (mode == QStringLiteral("observe-ready")) {
            QTRY_COMPARE_WITH_TIMEOUT(core.aggregateState(), QStringLiteral("Ready"), 10000);
            QTRY_COMPARE_WITH_TIMEOUT(circle->property("state").toString(), QStringLiteral("Ready"), 3000);
            QTRY_VERIFY_WITH_TIMEOUT(visualItemByName(window->contentItem(), "roleRow") != nullptr, 3000);
            return;
        }
        QCOMPARE(mode, QStringLiteral("lifecycle"));

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
        QVERIFY(coreStatus->property("text").toString().startsWith(QStringLiteral("Ready · r")));
        QVERIFY(aggregateStates.contains(QStringLiteral("Connecting")) ||
                aggregateStates.contains(QStringLiteral("Ready")));

        const RoleSnapshot role = core.roles()->roleAt(0);
        QVERIFY(!role.protocol.isEmpty());
        QVERIFY(!role.interfaceName.isEmpty());
        QVERIFY(role.state == QStringLiteral("Ready") || role.state == QStringLiteral("Online"));
        QTRY_VERIFY_WITH_TIMEOUT(visualItemByName(window->contentItem(), "roleRow") != nullptr, 3000);

        const auto tabs = visualItemsByName(window->contentItem(), "bottomTabButton");
        // Connect, Roles, Routing, Settings and Diagnostics: U12 added the fifth
        // tab, which this test never noticed while it was DISABLED.
        QCOMPARE(tabs.size(), 5);
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
};

QTEST_MAIN(RealCoreUiTest)
#include "RealCoreUiTest.moc"
