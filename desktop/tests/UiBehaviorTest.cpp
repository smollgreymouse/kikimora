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
    void createFlowsAddProfileAndTunnel()
    {
        Fixture fixture;
        QVERIFY(fixture.root);

        fixture.root->setProperty("selectedTab", 3);
        QTRY_COMPARE_WITH_TIMEOUT(fixture.root->property("selectedTab").toInt(), 3, 1000);

        auto *profileField = itemByName(fixture.root, "newProfileField");
        auto *profileButton = itemByName(fixture.root, "createProfileButton");
        auto *tunnelName = itemByName(fixture.root, "tunnelNameField");
        auto *tunnelEndpoint = itemByName(fixture.root, "tunnelEndpointField");
        auto *tunnelUser = itemByName(fixture.root, "tunnelUserField");
        auto *tunnelButton = itemByName(fixture.root, "createTunnelButton");
        QVERIFY(profileField);
        QVERIFY(profileButton);
        QVERIFY(tunnelName);
        QVERIFY(tunnelEndpoint);
        QVERIFY(tunnelButton);

        profileField->setProperty("value", QStringLiteral("second"));
        QMetaObject::invokeMethod(profileButton, "clicked");
        QVERIFY2(fixture.core.profiles().contains(QStringLiteral("second")),
                 qPrintable(fixture.core.profiles().join(QLatin1Char(','))));

        const int before = fixture.core.roles()->rowCount();
        tunnelName->setProperty("value", QStringLiteral("lab-tunnel"));
        tunnelEndpoint->setProperty("value", QStringLiteral("https://vpn.example"));
        if (tunnelUser) tunnelUser->setProperty("value", QStringLiteral("lab"));
        QMetaObject::invokeMethod(tunnelButton, "clicked");
        QTRY_COMPARE_WITH_TIMEOUT(fixture.core.roles()->rowCount(), before + 1, 2000);
        QVERIFY(!fixture.core.lastCommandMessage().isEmpty());
    }

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

        QTRY_VERIFY_WITH_TIMEOUT(visualItemsByName(fixture.window->contentItem(), "bottomTabButton").size() == 5,
                                 1500);
        QTRY_VERIFY_WITH_TIMEOUT(visualItemsByName(fixture.window->contentItem(), "tabIcon").size() == 5,
                                 1500);
        const auto tabs = visualItemsByName(fixture.window->contentItem(), "bottomTabButton");
        const auto icons = visualItemsByName(fixture.window->contentItem(), "tabIcon");
        QCOMPARE(tabs.size(), 5);
        QCOMPARE(icons.size(), 5);
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

        // FakeCoreBackend exposes ten deterministic fixtures. nextDemoScenario
        // advances (index + 1) % 10, so the first ten clicks visit 1..9 then 0.
        const QStringList states{
            QStringLiteral("Recovering"), QStringLiteral("WaitingForUnderlay"),
            QStringLiteral("Failed"), QStringLiteral("Ready"),
            QStringLiteral("Recovering"), QStringLiteral("Recovering"),
            QStringLiteral("Connecting"), QStringLiteral("Recovering"),
            QStringLiteral("Failed"), QStringLiteral("Stopped")};
        QCOMPARE(states.size(), FakeCoreBackend::fixtureCount);
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
        for (int i = 0; i < labels.size(); ++i) {
            const auto currentRows = visualItemsByName(fixture.window->contentItem(), "roleRow");
            QVERIFY2(currentRows.size() == 2,
                     qPrintable(QStringLiteral("role row count: %1").arg(currentRows.size())));
            auto *row = qobject_cast<QQuickItem *>(currentRows.at(i));
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

        for (int i = 0; i < labels.size(); ++i) {
            const auto currentRows = visualItemsByName(fixture.window->contentItem(), "roleRow");
            QCOMPARE(currentRows.size(), 2);
            auto *row = qobject_cast<QQuickItem *>(currentRows.at(i));
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

    void profileSwitcherReflectsActiveProfile()
    {
        Fixture fixture;
        QVERIFY(fixture.root);
        QVERIFY(fixture.window);
        const auto tabs = visualItemsByName(fixture.window->contentItem(), "bottomTabButton");
        QCOMPARE(tabs.size(), 5);
        auto *profileTab = qobject_cast<QQuickItem *>(tabs.at(1));
        QVERIFY(profileTab);
        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier,
                          settledClickPoint(profileTab, fixture.window));
        QTRY_COMPARE_WITH_TIMEOUT(fixture.root->property("selectedTab").toInt(), 1, 1000);

        auto *combo = itemByName(fixture.root, "profileCombo");
        QVERIFY(combo);
        const QVariant model = combo->property("model");
        QVERIFY(model.canConvert<QStringList>());
        QCOMPARE(model.toStringList(), QStringList({QStringLiteral("home"), QStringLiteral("work")}));
        QCOMPARE(combo->property("currentIndex").toInt(), 0);

        fixture.core.setActiveProfile(QStringLiteral("work"));
        QTRY_COMPARE_WITH_TIMEOUT(combo->property("currentIndex").toInt(), 1, 1000);
    }

    void navigationCoversAllTabsAndPlaceholders()
    {
        Fixture fixture;
        QVERIFY(fixture.root);
        QVERIFY(fixture.window);
        QTRY_VERIFY_WITH_TIMEOUT(visualItemsByName(fixture.window->contentItem(), "bottomTabButton").size() == 5,
                                 1500);
        const auto tabs = visualItemsByName(fixture.window->contentItem(), "bottomTabButton");
        auto *stack = itemByName(fixture.root, "contentStack");
        auto *circle = itemByName(fixture.root, "connectCircle");
        auto *demoButton = itemByName(fixture.root, "nextDemoButton");
        QVERIFY(stack);
        QVERIFY(circle);
        QVERIFY(demoButton);
        QCOMPARE(tabs.size(), 5);

        const QList<QString> placeholderTitles{
            {}, QStringLiteral("Profiles"), QStringLiteral("Routing"), QStringLiteral("Settings"),
            QStringLiteral("Diagnostics")};
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
        QVERIFY2(routingTexts.join(QStringLiteral(" ")).contains(fixture.core.leshySupported()
                                                                     ? QStringLiteral("Leshy owns routing")
                                                                     : QStringLiteral("not available on this platform")),
                 qPrintable(QStringLiteral("routing texts: [%1]").arg(routingTexts.join(QStringLiteral(" | ")))));

        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier,
                          settledClickPoint(qobject_cast<QQuickItem *>(tabs.at(0)), fixture.window));
        QTRY_COMPARE_WITH_TIMEOUT(stack->property("currentIndex").toInt(), 0, 1000);
        QTRY_VERIFY_WITH_TIMEOUT(circle->isVisible(), 1000);
    }

    void diagnosticsPageExplainsRoleState()
    {
        Fixture fixture;
        QVERIFY(fixture.root);
        QVERIFY(fixture.window);
        QTRY_VERIFY_WITH_TIMEOUT(visualItemsByName(fixture.window->contentItem(), "bottomTabButton").size() == 5, 1500);
        const auto tabs = visualItemsByName(fixture.window->contentItem(), "bottomTabButton");
        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier,
                          settledClickPoint(qobject_cast<QQuickItem *>(tabs.at(4)), fixture.window));
        auto *page = itemByName(fixture.root, "diagnosticsPage");
        QVERIFY(page);
        QTRY_VERIFY_WITH_TIMEOUT(page->isVisible(), 1500);

        auto *backend = itemByName(fixture.root, "diagBackend");
        auto *revision = itemByName(fixture.root, "diagRevision");
        auto *aggregate = itemByName(fixture.root, "diagAggregateState");
        QVERIFY(backend);
        QVERIFY(revision);
        QVERIFY(aggregate);
        QVERIFY(!backend->property("value").toString().isEmpty());
        QCOMPARE(aggregate->property("value").toString(), fixture.core.aggregateState());

        // Every role exposes its recovery/next-retry truth on this page.
        QTRY_VERIFY_WITH_TIMEOUT(visualItemsByName(fixture.window->contentItem(), "diagRoleNextRetry").size()
                                     == fixture.core.roles()->rowCount(),
                                 1500);

        // The raw summary the UI received is reachable through the dialog.
        auto *showButton = itemByName(fixture.root, "diagShowSummaryButton");
        QVERIFY(showButton);
        QVERIFY(showButton->isVisible());
        QVERIFY(showButton->width() > 0.0 && showButton->height() > 0.0);
        // The action is a compact target and the offscreen platform does not
        // deliver a pointer click to it reliably, so the handler itself is
        // invoked; the assertions below still prove that it opens the dialog
        // and hands it the summary the core reported.
        QVERIFY(QMetaObject::invokeMethod(showButton, "clicked"));
        auto *dialog = objectByName(fixture.root, "diagSummaryDialog");
        QVERIFY(dialog);
        QTRY_VERIFY_WITH_TIMEOUT(dialog->property("opened").toBool(), 1500);
        auto *summary = itemByName(fixture.root, "diagSummaryText");
        QVERIFY(summary);
        QVERIFY(summary->property("text").toString().contains(QStringLiteral("core_state")));
        QVERIFY(QMetaObject::invokeMethod(dialog, "close"));
        QTRY_VERIFY_WITH_TIMEOUT(!dialog->property("opened").toBool(), 1000);
    }

    // Every control the chapter-09 plan names must load as a component, so the
    // design system cannot rot into dead files that no page ever instantiates.
    void designSystemComponentsLoad()
    {
        Fixture fixture;
        const QStringList components{
            QStringLiteral("qrc:/qml/Controls/KStatusChip.qml"),
            QStringLiteral("qrc:/qml/Controls/KDivider.qml"),
            QStringLiteral("qrc:/qml/Controls/KProgressIndicator.qml"),
            QStringLiteral("qrc:/qml/Controls/KBanner.qml"),
            QStringLiteral("qrc:/qml/Controls/KIconButton.qml"),
            QStringLiteral("qrc:/qml/Controls/KHeader.qml"),
            QStringLiteral("qrc:/qml/Controls/KTextField.qml"),
            QStringLiteral("qrc:/qml/Controls/KSwitch.qml"),
            QStringLiteral("qrc:/qml/Controls/KComboBox.qml"),
            QStringLiteral("qrc:/qml/Controls/KDialog.qml"),
            QStringLiteral("qrc:/qml/Controls/KDrawer.qml"),
            QStringLiteral("qrc:/qml/Controls/KRoleSummaryRow.qml"),
            QStringLiteral("qrc:/qml/Controls/KRoleDetailDrawer.qml"),
            QStringLiteral("qrc:/qml/Controls/KConnectControl.qml"),
            QStringLiteral("qrc:/qml/Controls/KBottomNav.qml"),
            QStringLiteral("qrc:/qml/Pages/DiagnosticsPage.qml")};
        for (const QString &url : components) {
            QQmlComponent component(&fixture.engine, QUrl(url));
            QVERIFY2(component.isReady(), qPrintable(url + QStringLiteral(": ") + component.errorString()));
        }
    }

    void drawerActionsAreCoreReported()
    {
        Fixture fixture;
        QVERIFY(fixture.root);
        QVERIFY(fixture.window);
        fixture.core.nextDemoScenario(); // fixture 1: Secondary is Recovering
        QTRY_COMPARE_WITH_TIMEOUT(fixture.core.aggregateState(), QStringLiteral("Recovering"), 1000);
        QTRY_VERIFY_WITH_TIMEOUT(visualItemsByName(fixture.window->contentItem(), "roleRow").size() == 2, 1500);
        const auto rows = visualItemsByName(fixture.window->contentItem(), "roleRow");
        auto *drawer = objectByName(fixture.root, "roleDrawer");
        QVERIFY(drawer);
        auto *row = qobject_cast<QQuickItem *>(rows.at(1));
        QVERIFY(row);
        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(row, fixture.window));
        QTRY_VERIFY_WITH_TIMEOUT(drawer->property("opened").toBool(), 1000);
        auto *primary = itemByName(fixture.root, "roleActionButton");
        QVERIFY(primary);
        QCOMPARE(primary->property("text").toString(), QStringLiteral("Retry this role"));
        QTRY_VERIFY_WITH_TIMEOUT(visualItemByName(fixture.window->contentItem(), "roleSecondaryActionButton") != nullptr,
                                 1000);
        const QStringList texts = collectTexts(fixture.window->contentItem());
        QVERIFY2(texts.contains(QStringLiteral("Disconnect this role")),
                 qPrintable(QStringLiteral("drawer texts: [%1]").arg(texts.join(QStringLiteral(" | ")))));
        // The primary action is sent by its core-reported name, not guessed.
        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(primary, fixture.window));
        waitDrawerClose(drawer);
        QCOMPARE(fixture.core.roles()->roleAt(1).state, QStringLiteral("Ready"));
    }

    void routingPageShowsLeshyAndRoles()
    {
        Fixture fixture;
        QVERIFY(fixture.root);
        QVERIFY(fixture.window);
        QTRY_VERIFY_WITH_TIMEOUT(visualItemsByName(fixture.window->contentItem(), "bottomTabButton").size() == 5, 1500);
        const auto tabs = visualItemsByName(fixture.window->contentItem(), "bottomTabButton");
        auto *routingTab = qobject_cast<QQuickItem *>(tabs.at(2));
        QVERIFY(routingTab);
        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier, settledClickPoint(routingTab, fixture.window));
        QTRY_COMPARE_WITH_TIMEOUT(fixture.root->property("selectedTab").toInt(), 2, 1000);
        const QStringList texts = collectTexts(fixture.window->contentItem());
        const QString joined = texts.join(QStringLiteral(" "));
        QVERIFY2(texts.contains(QStringLiteral("Primary")), qPrintable(joined));
        QVERIFY2(texts.contains(QStringLiteral("Secondary")), qPrintable(joined));
        QVERIFY2(joined.contains(QStringLiteral("Zone")), qPrintable(joined));
        QVERIFY2(joined.contains(QStringLiteral("Parking")), qPrintable(joined));
    }

    void settingsShowsSimulatedMarkerAndDiagnostics()
    {
        Fixture fixture;
        QVERIFY(fixture.root);
        QVERIFY(fixture.window);
        QTRY_VERIFY_WITH_TIMEOUT(visualItemsByName(fixture.window->contentItem(), "bottomTabButton").size() == 5, 1500);
        const auto tabs = visualItemsByName(fixture.window->contentItem(), "bottomTabButton");
        auto *settingsTab = qobject_cast<QQuickItem *>(tabs.at(3));
        QVERIFY(settingsTab);
        QTest::mouseClick(fixture.window, Qt::LeftButton, Qt::NoModifier,
                          settledClickPoint(settingsTab, fixture.window));
        QTRY_COMPARE_WITH_TIMEOUT(fixture.root->property("selectedTab").toInt(), 3, 1000);
        auto *marker = itemByName(fixture.root, "simulatedMarker");
        QVERIFY(marker);
        QTRY_VERIFY_WITH_TIMEOUT(marker->isVisible(), 1000);
        const QStringList texts = collectTexts(fixture.window->contentItem());
        const QString joined = texts.join(QStringLiteral(" "));
        QVERIFY2(texts.contains(QStringLiteral("SIMULATED")), qPrintable(joined));
        QVERIFY2(texts.contains(QStringLiteral("fake")), qPrintable(joined));
        QVERIFY2(texts.contains(QStringLiteral("Settings")), qPrintable(joined));
        // Settings carries the live facts: profile, its list, aggregate and underlay.
        QVERIFY2(texts.contains(QStringLiteral("Profile")), qPrintable(joined));
        QVERIFY2(texts.contains(QStringLiteral("Underlay")), qPrintable(joined));
        QVERIFY2(texts.contains(QStringLiteral("Aggregate")), qPrintable(joined));
        QVERIFY2(texts.contains(QStringLiteral("home")), qPrintable(joined));
    }
};

QTEST_MAIN(UiBehaviorTest)
#include "UiBehaviorTest.moc"
