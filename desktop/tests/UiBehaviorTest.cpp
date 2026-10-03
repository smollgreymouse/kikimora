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

        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, clickPoint(role, fixture.window));
        QTRY_VERIFY_WITH_TIMEOUT(drawer->property("opened").toBool(), 1000);
        auto *title = itemByName(fixture.root, "drawerRoleTitle");
        auto *action = itemByName(fixture.root, "roleActionButton");
        QVERIFY(title);
        QVERIFY(action);
        QCOMPARE(title->property("text").toString(), QStringLiteral("Primary"));
        QCOMPARE(action->property("text").toString(), QStringLiteral("Connect this role"));

        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, clickPoint(action, fixture.window));
        QTRY_COMPARE_WITH_TIMEOUT(fixture.core.roles()->roleAt(0).state, QStringLiteral("Ready"), 1000);
        QTRY_VERIFY_WITH_TIMEOUT(!drawer->property("opened").toBool(), 1000);
        QTRY_COMPARE_WITH_TIMEOUT(fixture.core.aggregateState(), QStringLiteral("Recovering"), 1000);
    }
};

QTEST_MAIN(UiBehaviorTest)
#include "UiBehaviorTest.moc"
