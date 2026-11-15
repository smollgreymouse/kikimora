#include "support/UiTestFixture.h"
#include "support/QmlWarningGuard.h"
#include "core/FakeCoreBackend.h"

#include <QAccessible>
#include <QApplication>
#include <QQuickItem>
#include <QQuickWindow>
#include <QTest>

// U10 §13.1/§13.2 (docs/desktop-ui-executor-coverage-plan-2026-11-11.md):
// accessibility and geometry assertions against the production QML tree driven by
// the FakeCore backend.
//
// Accessibility is read through QAccessible::queryAccessibleInterface, i.e. the
// same surface assistive technology uses. Reading the QML "Accessible.name"
// property directly does NOT work: Accessible is an attached property on the item
// and is not exposed as a plain dynamic property, so a direct read returns an
// invalid QVariant for every control (verified: it reported role 0 for controls
// that do declare Accessible.role).
//
// A fresh FakeCoreBackend starts api-compatible, snapshot-authoritative and
// startup-ready (FakeCoreBackend.h:125-127), so it reports safeToMutate == true.
// Tests that need an unsafe state drive it explicitly with setApiCompatible /
// setStartupReady / setModeTransition rather than assuming a cold default.

namespace {

QQuickItem *visualByName(QQuickItem *root, const QString &name)
{
    if (!root) return nullptr;
    if (root->objectName() == name) return root;
    for (QQuickItem *child : root->childItems()) {
        if (auto *match = visualByName(child, name)) return match;
    }
    return nullptr;
}

QList<QQuickItem *> visualAllByName(QQuickItem *root, const QString &name)
{
    QList<QQuickItem *> matches;
    if (!root) return matches;
    if (root->objectName() == name) matches.append(root);
    for (QQuickItem *child : root->childItems()) matches.append(visualAllByName(child, name));
    return matches;
}

// The accessibility surface for one item, or nullptr when the item exposes none.
QAccessibleInterface *accessibleFor(QQuickItem *item)
{
    if (!item) return nullptr;
    return QAccessible::queryAccessibleInterface(item);
}

} // namespace

class AccessibilityUiTest : public QObject
{
    Q_OBJECT

private slots:
    void initTestCase();
    void init();
    void cleanup();

    void connectControlIsAnAccessibleButtonWithAName();
    void bottomNavigationExposesBothTabs();
    void safetyBannerIsAnAlertWithText();
    void unsafeStateDisablesTheConnectControl();
    void profileSelectorAndRoleListArePresent();

    void geometryAtReferenceSizes();
    void tabMovesFocusToARealControl();

private:
    void assertGeometryAt(int width, int height);
    // True when the accessibility layer reports this item as named and, when
    // expectedRole is not NoRole, with that role.
    static bool hasAccessibleIdentity(QQuickItem *item, QAccessible::Role expectedRole);
};

bool AccessibilityUiTest::hasAccessibleIdentity(QQuickItem *item, QAccessible::Role expectedRole)
{
    QAccessibleInterface *iface = accessibleFor(item);
    if (!iface) return false;
    if (expectedRole != QAccessible::NoRole && iface->role() != expectedRole) return false;
    const QString name = iface->text(QAccessible::Name);
    return !name.trimmed().isEmpty();
}

void AccessibilityUiTest::initTestCase()
{
    UiTest::ensureProductionBootstrap();
    UiTest::QmlWarningGuard::instance().install();
}

void AccessibilityUiTest::init()
{
    UiTest::QmlWarningGuard::instance().reset();
}

void AccessibilityUiTest::cleanup()
{
    const QStringList unexpected = UiTest::QmlWarningGuard::instance().unexpectedMessages();
    QVERIFY2(unexpected.isEmpty(),
             qPrintable(QStringLiteral("unexpected QML warnings:\n%1")
                            .arg(unexpected.join(QStringLiteral("\n")))));
}

void AccessibilityUiTest::connectControlIsAnAccessibleButtonWithAName()
{
    UiTest::UiTestFixture<FakeCoreBackend> fixture;
    QVERIFY2(fixture.root, "qrc:/qml/Main.qml did not load");

    // The Accessible properties live on KConnectControl, whose objectName in
    // Main.qml is "connectArea"; "connectCircle" is the inner ring item and
    // deliberately carries no accessibility identity of its own.
    QQuickItem *connect = visualByName(fixture.window->contentItem(),
                                       QStringLiteral("connectArea"));
    QVERIFY2(connect, "the main connect control is not in the tree");
    QVERIFY2(hasAccessibleIdentity(connect, QAccessible::Button),
             "the main connect control does not expose an accessible button with a name");
}

void AccessibilityUiTest::bottomNavigationExposesBothTabs()
{
    UiTest::UiTestFixture<FakeCoreBackend> fixture;
    QVERIFY(fixture.root);

    const auto tabs = visualAllByName(fixture.window->contentItem(),
                                      QStringLiteral("bottomTabButton"));
    QCOMPARE(tabs.size(), 2);
    for (QQuickItem *tab : tabs) {
        QVERIFY2(hasAccessibleIdentity(tab, QAccessible::PageTab),
                 "a navigation tab does not expose an accessible page tab with a name");
    }
}

void AccessibilityUiTest::safetyBannerIsAnAlertWithText()
{
    UiTest::UiTestFixture<FakeCoreBackend> fixture;
    QVERIFY(fixture.root);

    // Priority one of the fail-closed banner: an incompatible API.
    fixture.core.setApiCompatible(false);
    QQuickItem *banner = visualByName(fixture.window->contentItem(),
                                      QStringLiteral("routeAuthorityBanner"));
    QVERIFY(banner);
    QTRY_VERIFY_WITH_TIMEOUT(banner->isVisible(), 3000);
    QVERIFY2(hasAccessibleIdentity(banner, QAccessible::AlertMessage),
             "the safety banner does not expose an accessible alert with meaningful text");
}

void AccessibilityUiTest::unsafeStateDisablesTheConnectControl()
{
    UiTest::UiTestFixture<FakeCoreBackend> fixture;
    QVERIFY(fixture.root);

    // A fresh fake is fully ready; force the unsafe state explicitly.
    QVERIFY2(fixture.core.safeToMutate(), "a fresh FakeCore is expected to be ready");
    fixture.core.setStartupReady(false);
    QVERIFY2(!fixture.core.safeToMutate(), "startup-not-ready must block mutation");

    QQuickItem *connect = visualByName(fixture.window->contentItem(),
                                       QStringLiteral("connectCircle"));
    QVERIFY(connect);
    QVERIFY2(!connect->isEnabled() || !connect->isVisible(),
             "the ring stays operable while mutation is unsafe");
}

void AccessibilityUiTest::profileSelectorAndRoleListArePresent()
{
    UiTest::UiTestFixture<FakeCoreBackend> fixture;
    QVERIFY(fixture.root);

    QQuickItem *selector = visualByName(fixture.window->contentItem(),
                                        QStringLiteral("profileSelector"));
    QVERIFY2(selector, "the profile selector is missing");

    QQuickItem *roles = visualByName(fixture.window->contentItem(),
                                     QStringLiteral("roleList"));
    QVERIFY2(roles, "the role list is missing");
}

void AccessibilityUiTest::assertGeometryAt(int width, int height)
{
    UiTest::UiTestFixture<FakeCoreBackend> fixture;
    QVERIFY(fixture.root);
    QQuickWindow *window = fixture.window;
    QVERIFY(window);
    window->resize(width, height);
    QTest::qWait(150);

    QQuickItem *content = window->contentItem();
    const QRectF bounds(0, 0, content->width(), content->height());

    const auto tabs = visualAllByName(content, QStringLiteral("bottomTabButton"));
    QCOMPARE(tabs.size(), 2);
    for (QQuickItem *tab : tabs) {
        QVERIFY2(tab->isVisible() && tab->width() > 0 && tab->height() > 0,
                 "a bottom tab collapsed");
    }

    QQuickItem *connect = visualByName(content, QStringLiteral("connectArea"));
    QVERIFY(connect);
    const QPointF center = connect->mapToItem(
        content, QPointF(connect->width() / 2.0, connect->height() / 2.0));
    QVERIFY2(bounds.contains(center), "the connect control is clipped out of the window");

    QQuickItem *nav = visualByName(content, QStringLiteral("bottomNav"));
    QVERIFY2(nav, "the bottom navigation is missing");
    QVERIFY2(nav->height() > 0, "the bottom navigation collapsed to zero height");

    // The nav rectangle must lie inside the window. Compare against the rect's
    // bottom-right corner with a small tolerance rather than QRectF::contains on
    // the exact edge, which is exclusive for the far boundary.
    const QPointF navTopLeft = nav->mapToItem(content, QPointF(0, 0));
    const QPointF navBottomRight = nav->mapToItem(content, QPointF(nav->width(), nav->height()));
    QVERIFY2(bounds.adjusted(-1, -1, 1, 1).contains(navTopLeft),
             "the bottom navigation starts outside the window");
    QVERIFY2(bounds.adjusted(-1, -1, 1, 1).contains(navBottomRight),
             "the bottom navigation extends past the window");
}

void AccessibilityUiTest::geometryAtReferenceSizes()
{
    // §13.2: 360x720 is the pinned reference, the other two are geometry-only.
    assertGeometryAt(340, 620);
    assertGeometryAt(360, 720);
    assertGeometryAt(480, 800);
}

void AccessibilityUiTest::tabMovesFocusToARealControl()
{
    UiTest::UiTestFixture<FakeCoreBackend> fixture;
    QVERIFY(fixture.root);
    QQuickWindow *window = fixture.window;
    QVERIFY(window);
    window->resize(360, 720);
    QTest::qWait(150);
    window->requestActivate();
    QTest::qWait(50);

    QTest::keyClick(window, Qt::Key_Tab);
    QTest::qWait(80);
    QQuickItem *focused = window->activeFocusItem();
    QVERIFY2(focused, "Tab left no item focused");
    QVERIFY2(focused->isVisible() && focused->isEnabled(),
             "Tab focused an invisible or disabled item");
}

QTEST_MAIN(AccessibilityUiTest)
#include "AccessibilityUiTest.moc"
