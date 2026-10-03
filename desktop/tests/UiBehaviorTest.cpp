#include "DesktopIntegration.h"
#include "SystemIconProvider.h"
#include "core/FakeCoreBackend.h"

#include <QApplication>
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

// Layout assigns geometry a frame after item creation; wait until the item's
// center lands inside the window before dispatching the mouse event. If it
// never settles, the click misses and the follow-up QTRY reports the gap.
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

void collectTexts(QQuickItem *root, QStringList &out)
{
    if (!root) return;
    const QVariant text = root->property("text");
    if (text.metaType().id() == QMetaType::QString) out.append(text.toString());
    for (QQuickItem *child : root->childItems()) collectTexts(child, out);
}

// Repeater delegates have no QObject parent, so the visual tree must be
// traversed (childItems) rather than the QObject ownership tree.
QStringList collectTexts(QQuickItem *root)
{
    QStringList texts;
    collectTexts(root, texts);
    return texts;
}

QString visibleTitleText(QQuickItem *root)
{
    if (!root) return {};
    if (root->isVisible() && root->property("titleText").isValid())
        return root->property("titleText").toString();
    for (QQuickItem *child : root->childItems()) {
        const QString found = visibleTitleText(child);
        if (!found.isEmpty()) return found;
    }
    return {};
}

void waitDrawerClose(QObject *drawer)
{
    QTRY_VERIFY_WITH_TIMEOUT(!drawer->property("opened").toBool(), 1000);
    QTest::qWait(350);
}
}

class UiBehaviorTest final : public QObject
{
    Q_OBJECT

private:
    struct Fixture {
        FakeCoreBackend core;
        DesktopIntegration desktop{&core};
        QQmlApplicationEngine engine;
        QObject *root = nullptr;
        QQuickWindow *window = nullptr;

        Fixture()
        {
            engine.addImageProvider(QStringLiteral("system"), new SystemIconProvider);
            engine.rootContext()->setContextProperty(QStringLiteral("Core"), &core);
            engine.rootContext()->setContextProperty(QStringLiteral("Desktop"), &desktop);
            engine.load(QUrl(QStringLiteral("qrc:/qml/Main.qml")));
            if (!engine.rootObjects().isEmpty()) {
                root = engine.rootObjects().constFirst();
                window = qobject_cast<QQuickWindow *>(root);
            }
        }
    };

private slots:
    void layoutNavigationAndIcons()
    {
        Fixture fixture;
        QVERIFY(fixture.root);
        QVERIFY(fixture.window);
        QCOMPARE(fixture.root->property("width").toInt(), 360);
        QCOMPARE(fixture.root->property("height").toInt(), 720);

        auto *circle = itemByName(fixture.root, "connectCircle");
        auto *circleHost = itemByName(fixture.root, "connectCircleHost");
        QVERIFY(circle);
        QVERIFY(circleHost);
        QVERIFY(qAbs((circle->x() + circle->width() / 2.0) - circleHost->width() / 2.0) < 1.0);
        QVERIFY(qAbs((circle->y() + circle->height() / 2.0) - circleHost->height() / 2.0) < 1.0);

        QTRY_VERIFY_WITH_TIMEOUT(visualItemsByName(fixture.window->contentItem(), "bottomTabButton").size() == 4,
                                 1500);
        QTRY_VERIFY_WITH_TIMEOUT(visualItemsByName(fixture.window->contentItem(), "tabIcon").size() == 4,
                                 1500);
        const auto tabs = visualItemsByName(fixture.window->contentItem(), "bottomTabButton");
        const auto icons = visualItemsByName(fixture.window->contentItem(), "tabIcon");
        QCOMPARE(tabs.size(), 4);
        QCOMPARE(icons.size(), 4);
        for (QObject *icon : icons)
            QVERIFY(icon->property("source").toUrl().toString().startsWith(QStringLiteral("image://system/")));

        auto *secondTab = qobject_cast<QQuickItem *>(tabs.at(1));
        auto *thirdTab = qobject_cast<QQuickItem *>(tabs.at(2));
        QVERIFY(secondTab);
        QVERIFY(thirdTab);
        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, clickPoint(secondTab, fixture.window));
        QTRY_COMPARE_WITH_TIMEOUT(fixture.root->property("selectedTab").toInt(), 1, 1000);
        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, clickPoint(thirdTab, fixture.window));
        QTRY_COMPARE_WITH_TIMEOUT(fixture.root->property("selectedTab").toInt(), 2, 1000);
    }

    void demoStatesReachVisibleControls()
    {
        Fixture fixture;
        QVERIFY(fixture.root);
        QVERIFY(fixture.window);
        auto *demoButton = itemByName(fixture.root, "nextDemoButton");
        auto *circle = itemByName(fixture.root, "connectCircle");
        auto *underlay = itemByName(fixture.root, "underlaySummary");
        QVERIFY(demoButton);
        QVERIFY(circle);
        QVERIFY(underlay);

        const QStringList states{
            QStringLiteral("Recovering"), QStringLiteral("WaitingForUnderlay"),
            QStringLiteral("Failed"), QStringLiteral("Stopped")};
        for (const QString &expected : states) {
            QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier,
                              clickPoint(demoButton, fixture.window));
            QTRY_COMPARE_WITH_TIMEOUT(fixture.core.aggregateState(), expected, 1500);
            QTRY_COMPARE_WITH_TIMEOUT(circle->property("state").toString(), expected, 1000);
            QVERIFY(!circle->property("stateText").toString().isEmpty());
            QVERIFY(!underlay->property("text").toString().isEmpty());
        }
    }

    void drawerOpensAndControlsOneRole()
    {
        Fixture fixture;
        QVERIFY(fixture.root);
        QVERIFY(fixture.window);
        QTRY_VERIFY_WITH_TIMEOUT(visualItemByName(fixture.window->contentItem(), "roleRow") != nullptr, 1500);
        auto *role = visualItemByName(fixture.window->contentItem(), "roleRow");
        auto *drawer = objectByName(fixture.root, "roleDrawer");
        QVERIFY(role);
        QVERIFY(drawer);

        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(role, fixture.window));
        QTRY_VERIFY_WITH_TIMEOUT(drawer->property("opened").toBool(), 1000);
        auto *title = itemByName(fixture.root, "drawerRoleTitle");
        auto *action = itemByName(fixture.root, "roleActionButton");
        QVERIFY(title);
        QVERIFY(action);
        QCOMPARE(title->property("text").toString(), QStringLiteral("Primary"));
        QCOMPARE(action->property("text").toString(), QStringLiteral("Connect this role"));

        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(action, fixture.window));
        QTRY_COMPARE_WITH_TIMEOUT(fixture.core.roles()->roleAt(0).state, QStringLiteral("Ready"), 1000);
        QTRY_VERIFY_WITH_TIMEOUT(!drawer->property("opened").toBool(), 1000);
        QTRY_COMPARE_WITH_TIMEOUT(fixture.core.aggregateState(), QStringLiteral("Recovering"), 1000);
    }

    void connectCircleDrivesGlobalLifecycle()
    {
        Fixture fixture;
        QVERIFY(fixture.root);
        QVERIFY(fixture.window);
        auto *circle = itemByName(fixture.root, "connectCircle");
        auto *status = itemByName(fixture.root, "coreStatus");
        auto *roleList = itemByName(fixture.root, "roleList");
        QVERIFY(circle);
        QVERIFY(status);
        QVERIFY(roleList);

        QCOMPARE(roleList->property("count").toInt(), 2);
        QTRY_VERIFY_WITH_TIMEOUT(visualItemsByName(fixture.window->contentItem(), "roleRow").size() == 2, 1500);

        QCOMPARE(fixture.core.aggregateState(), QStringLiteral("Stopped"));
        QCOMPARE(circle->property("state").toString(), QStringLiteral("Stopped"));
        QCOMPARE(circle->property("actionText").toString(), QStringLiteral("CONNECT"));

        QVector<qulonglong> revisions;
        connect(&fixture.core, &CoreBackend::snapshotChanged, this,
                [&] { revisions.append(fixture.core.revision()); });

        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(circle, fixture.window));
        QTRY_COMPARE_WITH_TIMEOUT(fixture.core.aggregateState(), QStringLiteral("Ready"), 5000);
        QTRY_COMPARE_WITH_TIMEOUT(circle->property("state").toString(), QStringLiteral("Ready"), 3000);
        QCOMPARE(circle->property("actionText").toString(), QStringLiteral("DISCONNECT"));
        const QString statusText = status->property("text").toString();
        QVERIFY2(statusText.startsWith(QStringLiteral("Ready")),
                 qPrintable(QStringLiteral("core status badge not Ready: %1").arg(statusText)));
        QVERIFY2(statusText.contains(QString::number(fixture.core.revision())),
                 qPrintable(QStringLiteral("core status badge missing revision: %1").arg(statusText)));
        QCOMPARE(fixture.core.roles()->roleAt(0).state, QStringLiteral("Ready"));
        QCOMPARE(fixture.core.roles()->roleAt(1).state, QStringLiteral("Ready"));
        QCOMPARE(fixture.core.roles()->roleAt(0).leshyPublished, fixture.core.leshySupported());

        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(circle, fixture.window));
        QTRY_COMPARE_WITH_TIMEOUT(fixture.core.aggregateState(), QStringLiteral("Stopped"), 5000);
        QTRY_COMPARE_WITH_TIMEOUT(circle->property("state").toString(), QStringLiteral("Stopped"), 3000);
        QCOMPARE(circle->property("actionText").toString(), QStringLiteral("CONNECT"));
        QCOMPARE(fixture.core.roles()->roleAt(0).state, QStringLiteral("Stopped"));
        QCOMPARE(fixture.core.roles()->roleAt(1).state, QStringLiteral("Stopped"));
        QVERIFY(!fixture.core.roles()->roleAt(0).leshyPublished);

        QVERIFY(revisions.size() >= 4);
        for (qsizetype i = 1; i < revisions.size(); ++i)
            QVERIFY2(revisions.at(i) > revisions.at(i - 1), "core revisions must be strictly increasing");
    }

    void roleAggregationAcrossRoles()
    {
        Fixture fixture;
        QVERIFY(fixture.root);
        QVERIFY(fixture.window);
        QTRY_VERIFY_WITH_TIMEOUT(visualItemsByName(fixture.window->contentItem(), "roleRow").size() == 2, 1500);
        const auto rows = visualItemsByName(fixture.window->contentItem(), "roleRow");
        auto *drawer = objectByName(fixture.root, "roleDrawer");
        auto *action = itemByName(fixture.root, "roleActionButton");
        auto *title = itemByName(fixture.root, "drawerRoleTitle");
        QVERIFY(drawer);
        QVERIFY(action);
        QVERIFY(title);

        const QList<QString> labels{QStringLiteral("Primary"), QStringLiteral("Secondary")};
        for (int i = 0; i < rows.size(); ++i) {
            auto *row = qobject_cast<QQuickItem *>(rows.at(i));
            QVERIFY(row);
            QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(row, fixture.window));
            QTRY_VERIFY_WITH_TIMEOUT(drawer->property("opened").toBool(), 1000);
            QCOMPARE(title->property("text").toString(), labels.at(i));
            QCOMPARE(action->property("text").toString(), QStringLiteral("Connect this role"));
            QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(action, fixture.window));
            waitDrawerClose(drawer);
        }
        QTRY_COMPARE_WITH_TIMEOUT(fixture.core.aggregateState(), QStringLiteral("Ready"), 1000);
        QCOMPARE(fixture.core.roles()->roleAt(0).state, QStringLiteral("Ready"));
        QCOMPARE(fixture.core.roles()->roleAt(1).state, QStringLiteral("Ready"));

        for (int i = 0; i < rows.size(); ++i) {
            auto *row = qobject_cast<QQuickItem *>(rows.at(i));
            QVERIFY(row);
            QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(row, fixture.window));
            QTRY_VERIFY_WITH_TIMEOUT(drawer->property("opened").toBool(), 1000);
            QCOMPARE(action->property("text").toString(), QStringLiteral("Disconnect this role"));
            QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(action, fixture.window));
            waitDrawerClose(drawer);
            QCOMPARE(fixture.core.aggregateState(), i == 0 ? QStringLiteral("Recovering")
                                                          : QStringLiteral("Stopped"));
            QCOMPARE(fixture.core.roles()->roleAt(i).state, QStringLiteral("Stopped"));
        }
        QCOMPARE(fixture.core.roles()->roleAt(0).state, QStringLiteral("Stopped"));
        QCOMPARE(fixture.core.roles()->roleAt(1).state, QStringLiteral("Stopped"));
    }

    void drawerShowsRoleDetailFields()
    {
        Fixture fixture;
        QVERIFY(fixture.root);
        QVERIFY(fixture.window);
        QTRY_VERIFY_WITH_TIMEOUT(visualItemByName(fixture.window->contentItem(), "roleRow") != nullptr, 1500);
        auto *role = visualItemByName(fixture.window->contentItem(), "roleRow");
        auto *drawer = objectByName(fixture.root, "roleDrawer");
        auto *action = itemByName(fixture.root, "roleActionButton");
        QVERIFY(role);
        QVERIFY(drawer);
        QVERIFY(action);

        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(role, fixture.window));
        QTRY_VERIFY_WITH_TIMEOUT(drawer->property("opened").toBool(), 1000);
        const QStringList fields = collectTexts(fixture.window->contentItem());
        QVERIFY(fields.contains(QStringLiteral("Primary")));
        QVERIFY2(fields.contains(QStringLiteral("amn0")),
                 qPrintable(QStringLiteral("drawer texts: [%1]").arg(fields.join(QStringLiteral(" | ")))));
        QVERIFY(fields.contains(QStringLiteral("wlan0 via 192.168.1.1")));
        QVERIFY(fields.contains(QStringLiteral("ready")));
        QVERIFY(fields.contains(QStringLiteral("Withdrawn")));
        QVERIFY(fields.contains(QStringLiteral("Connect this role")));

        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(action, fixture.window));
        waitDrawerClose(drawer);
        QCOMPARE(fixture.core.roles()->roleAt(0).state, QStringLiteral("Ready"));

        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(role, fixture.window));
        QTRY_VERIFY_WITH_TIMEOUT(drawer->property("opened").toBool(), 1000);
        const QStringList refreshed = collectTexts(fixture.window->contentItem());
        QVERIFY(refreshed.contains(QStringLiteral("Disconnect this role")));
        QVERIFY(refreshed.contains(QStringLiteral("Manual per-role connect")));
        QVERIFY(refreshed.contains(fixture.core.leshySupported() ? QStringLiteral("Published")
                                                                : QStringLiteral("Withdrawn")));
    }

    void navigationCoversAllTabsAndPlaceholders()
    {
        Fixture fixture;
        QVERIFY(fixture.root);
        QVERIFY(fixture.window);
        QTRY_VERIFY_WITH_TIMEOUT(visualItemsByName(fixture.window->contentItem(), "bottomTabButton").size() == 4,
                                 1500);
        const auto tabs = visualItemsByName(fixture.window->contentItem(), "bottomTabButton");
        auto *stack = itemByName(fixture.root, "contentStack");
        auto *circle = itemByName(fixture.root, "connectCircle");
        auto *demoButton = itemByName(fixture.root, "nextDemoButton");
        QVERIFY(stack);
        QVERIFY(circle);
        QVERIFY(demoButton);
        QCOMPARE(tabs.size(), 4);

        const QList<QString> placeholderTitles{
            {}, QStringLiteral("Profiles"), QStringLiteral("Routing"), QStringLiteral("Settings")};
        for (int i = 0; i < tabs.size(); ++i) {
            auto *tab = qobject_cast<QQuickItem *>(tabs.at(i));
            QVERIFY(tab);
            QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier,
                              settledClickPoint(tab, fixture.window));
            QTRY_COMPARE_WITH_TIMEOUT(fixture.root->property("selectedTab").toInt(), i, 1000);
            QTRY_COMPARE_WITH_TIMEOUT(stack->property("currentIndex").toInt(), i, 1000);
            QTRY_COMPARE_WITH_TIMEOUT(circle->isVisible(), i == 0, 1000);
            QTRY_COMPARE_WITH_TIMEOUT(demoButton->isVisible(), i == 0, 1000);
            if (i > 0)
                QCOMPARE(visibleTitleText(fixture.window->contentItem()), placeholderTitles.at(i));
        }

        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier,
                          settledClickPoint(qobject_cast<QQuickItem *>(tabs.at(2)), fixture.window));
        QTRY_COMPARE_WITH_TIMEOUT(stack->property("currentIndex").toInt(), 2, 1000);
        const QStringList routingTexts = collectTexts(stack);
        QVERIFY(routingTexts.join(QStringLiteral(" ")).contains(fixture.core.leshySupported()
                                                                    ? QStringLiteral("will appear here")
                                                                    : QStringLiteral("not available on this platform")));

        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier,
                          settledClickPoint(qobject_cast<QQuickItem *>(tabs.at(0)), fixture.window));
        QTRY_COMPARE_WITH_TIMEOUT(stack->property("currentIndex").toInt(), 0, 1000);
        QTRY_VERIFY_WITH_TIMEOUT(circle->isVisible(), 1000);
    }
};

QTEST_MAIN(UiBehaviorTest)
#include "UiBehaviorTest.moc"
