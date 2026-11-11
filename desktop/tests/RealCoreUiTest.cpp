#include "DesktopIntegration.h"
#include "support/UiTestFixture.h"
#include "support/QmlWarningGuard.h"
#include "core/RealCoreClient.h"

#include <QApplication>
#include <QFileInfo>
#include <QQuickItem>
#include <QQuickWindow>
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

    void realCoreEventsReachQml()
    {
        // Runs only where a live core exists (the lab VMs); everywhere else the
        // case skips so the suite stays green in CI.
        const QString socketPath = qEnvironmentVariable("KIKIMORA_UI_SOCKET");
        if (socketPath.isEmpty())
            QSKIP("KIKIMORA_UI_SOCKET is not set: no live core to drive");
        if (!QFileInfo::exists(socketPath))
            QSKIP("KIKIMORA_UI_SOCKET points at a missing socket");

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
