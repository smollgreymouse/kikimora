#include "support/UiTestFixture.h"
#include "support/QmlWarningGuard.h"
#include "core/FakeCoreBackend.h"

#include <QApplication>
#include <QDir>
#include <QFileInfo>
#include <QImage>
#include <QQuickItem>
#include <QQuickWindow>
#include <QTest>

// U10 §13.3 (docs/desktop-ui-executor-coverage-plan-2026-11-11.md): deterministic
// FakeCore screenshots in the pinned Linux lane.
//
// Rules from the packet that this file enforces:
//   - fixed locale, fixed font setup, software rendering, fixed window size;
//   - golden images live under desktop/tests/golden/linux-<qt-version>/ and images
//     from different Qt versions are never mixed;
//   - pixel comparison is allowed only on the pinned lane, with a documented
//     tolerance;
//   - Windows/macOS captures are evidence, not pixel-equality gates.
//
// Behaviour:
//   KIKIMORA_UI_CAPTURE_GOLDEN=1   write the reference images (blessing run)
//   default                        compare against the stored references
//
// The directory is derived from the running Qt version, so a 6.9.x workstation
// cannot silently compare against 6.8.3 references. The pinned CI lane is 6.8.3
// (.github/workflows/desktop-ui.yml:87).

namespace {

constexpr int kReferenceWidth = 360;
constexpr int kReferenceHeight = 720;

// Documented tolerance: the maximum allowed per-channel difference. Software
// rendering on one pinned Qt version is stable; the tolerance exists for text
// antialiasing, which still varies with the font backend.
constexpr int kTolerance = 24;

struct Capture
{
    const char *name;
    bool dark;
    bool diagnosticsTab;
    void (*drive)(FakeCoreBackend &);
};

// A fresh FakeCoreBackend reports aggregateState "Stopped", and disconnectAll()
// refuses only when disconnection is blocked, so the stopped state needs no driver.
void driveStopped(FakeCoreBackend &) {}

// Each remaining driver tells the wait loop which transient state it must leave:
// connectAll goes Connecting -> Ready, so the capture cannot happen mid-flight.
QString transientStateFor(const char *name)
{
    const QString n = QString::fromLatin1(name);
    return n.contains(QStringLiteral("stopped")) ? QString() : QStringLiteral("Connecting");
}

void driveReady(FakeCoreBackend &core)
{
    core.connectAll();
}

void driveAuthorityLost(FakeCoreBackend &core)
{
    core.connectAll();
    core.setManagedAuthority(true, false);
    core.setRouteAuthorityError(QStringLiteral("route authority lost"));
}

void driveModeTransition(FakeCoreBackend &core)
{
    core.connectAll();
    core.setModeTransition(QStringLiteral("config_written"));
}

void driveIpv6Unavailable(FakeCoreBackend &core)
{
    core.connectAll();
    core.setManagedAuthority(true, true);
    core.setRoleIpv6(0, false);
}

void driveDiagnostics(FakeCoreBackend &core)
{
    core.connectAll();
}

// The nine captures from §13.3, in the packet's order.
const Capture kCaptures[] = {
    {"home-stopped-light", false, false, driveStopped},
    {"home-ready-light", false, false, driveReady},
    {"home-authority-lost-light", false, false, driveAuthorityLost},
    {"home-mode-transition-light", false, false, driveModeTransition},
    {"home-ipv6-unavailable-light", false, false, driveIpv6Unavailable},
    {"settings-diagnostics-light", false, true, driveDiagnostics},
    {"home-stopped-dark", true, false, driveStopped},
    {"home-authority-lost-dark", true, false, driveAuthorityLost},
    {"settings-diagnostics-dark", true, true, driveDiagnostics},
};

QString goldenDir()
{
    return QStringLiteral(KIKIMORA_GOLDEN_ROOT) + QStringLiteral("/linux-") +
           QString::fromLatin1(qVersion());
}

// Compares two images with the documented tolerance and reports the worst channel
// delta, so a failure says how far off it is instead of just "different".
bool imagesMatch(const QImage &actual, const QImage &expected, QString *reason)
{
    if (actual.size() != expected.size()) {
        *reason = QStringLiteral("size %1x%2 vs %3x%4")
                      .arg(actual.width()).arg(actual.height())
                      .arg(expected.width()).arg(expected.height());
        return false;
    }
    const QImage a = actual.convertToFormat(QImage::Format_ARGB32);
    const QImage e = expected.convertToFormat(QImage::Format_ARGB32);
    int worst = 0;
    for (int y = 0; y < a.height(); ++y) {
        for (int x = 0; x < a.width(); ++x) {
            const QRgb pa = a.pixel(x, y);
            const QRgb pe = e.pixel(x, y);
            const int delta = qMax(qMax(qAbs(qRed(pa) - qRed(pe)),
                                        qAbs(qGreen(pa) - qGreen(pe))),
                                   qMax(qAbs(qBlue(pa) - qBlue(pe)),
                                        qAbs(qAlpha(pa) - qAlpha(pe))));
            worst = qMax(worst, delta);
            if (worst > kTolerance) {
                *reason = QStringLiteral("channel delta %1 > tolerance %2 at (%3,%4)")
                              .arg(worst).arg(kTolerance).arg(x).arg(y);
                return false;
            }
        }
    }
    *reason = worst > 0 ? QStringLiteral("within tolerance, worst delta %1").arg(worst)
                        : QStringLiteral("pixel identical");
    return true;
}

} // namespace

class VisualStateTest : public QObject
{
    Q_OBJECT

private slots:
    void initTestCase();
    void init();
    void cleanup();

    void capturesEveryNamedState();

private:
    void captureOne(const Capture &capture);
};

void VisualStateTest::initTestCase()
{
    UiTest::ensureProductionBootstrap();
    UiTest::QmlWarningGuard::instance().install();
    QDir().mkpath(goldenDir());
    qInfo() << "golden directory:" << goldenDir() << "lane Qt:" << qVersion()
            << "mode:" << (qEnvironmentVariableIsSet("KIKIMORA_UI_CAPTURE_GOLDEN")
                               ? "blessing (writes references)"
                               : "compare");
}

void VisualStateTest::init()
{
    UiTest::QmlWarningGuard::instance().reset();
}

void VisualStateTest::cleanup()
{
    const QStringList unexpected = UiTest::QmlWarningGuard::instance().unexpectedMessages();
    QVERIFY2(unexpected.isEmpty(),
             qPrintable(QStringLiteral("unexpected QML warnings:\n%1")
                            .arg(unexpected.join(QStringLiteral("\n")))));
}

void VisualStateTest::captureOne(const Capture &capture)
{
    UiTest::UiTestFixture<FakeCoreBackend> fixture;
    QVERIFY2(fixture.root, "qrc:/qml/Main.qml did not load");
    QQuickWindow *window = fixture.window;
    QVERIFY(window);

    // Theme and tab are set before the first settle so the frame that is captured
    // is generated with the intended state, not patched afterwards.
    fixture.theme.setDark(capture.dark);
    if (capture.diagnosticsTab) fixture.root->setProperty("selectedTab", 1);

    // Fixed geometry: the packet's 360x720 reference.
    window->resize(kReferenceWidth, kReferenceHeight);
    if (capture.drive) capture.drive(fixture.core);
    // connectAll()/disconnectAll() advance through QTimer::singleShot (850 ms in
    // FakeCoreBackend), so the event loop must run while waiting: QTRY_* pumps it,
    // a plain sleep would not. The transient state each driver must leave is
    // derived from the capture name so a stopped capture does not wait forever.
    const QString transient = transientStateFor(capture.name);
    if (!transient.isEmpty()) {
        QTRY_VERIFY_WITH_TIMEOUT(fixture.core.aggregateState() != transient, 15000);
    }
    QTest::qWait(400);

    const QImage actual = window->grabWindow();
    QVERIFY2(!actual.isNull(), "grabWindow() returned a null image");

    const QString path = goldenDir() + QLatin1Char('/') +
                         QString::fromLatin1(capture.name) + QStringLiteral(".png");

    if (qEnvironmentVariableIsSet("KIKIMORA_UI_CAPTURE_GOLDEN")) {
        QVERIFY2(actual.save(path), qPrintable(QStringLiteral("cannot write %1").arg(path)));
        qInfo() << "wrote reference" << path;
        return;
    }

    if (!QFileInfo::exists(path)) {
        QFAIL(qPrintable(QStringLiteral(
            "no reference image at %1 - run once with KIKIMORA_UI_CAPTURE_GOLDEN=1 on the "
            "pinned lane (Qt 6.8.3) to bless it").arg(path)));
    }
    const QImage expected(path);
    QVERIFY2(!expected.isNull(), qPrintable(QStringLiteral("cannot read %1").arg(path)));

    QString reason;
    const bool match = imagesMatch(actual, expected, &reason);
    QVERIFY2(match, qPrintable(QStringLiteral("%1: %2")
                                   .arg(QString::fromLatin1(capture.name), reason)));
}

void VisualStateTest::capturesEveryNamedState()
{
    for (const Capture &capture : kCaptures) {
        captureOne(capture);
    }
}

QTEST_MAIN(VisualStateTest)
#include "VisualStateTest.moc"
