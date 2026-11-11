#pragma once

#include <QStringList>
#include <QtGlobal>

// Test-failure semantics for Qt messages (U4, roadmap §11): unexpected QML
// runtime warnings are failures, not log decoration.
//
// Usage in a QtTest suite:
//   void initTestCase() { UiTest::QmlWarningGuard::instance().install(); }
//   void init()         { UiTest::QmlWarningGuard::instance().reset(); }
//   void cleanup()
//   {
//       const QStringList unexpected = UiTest::QmlWarningGuard::instance().unexpectedMessages();
//       QVERIFY2(unexpected.isEmpty(), qPrintable(QStringLiteral("unexpected QML warnings:\n%1")
//                                                     .arg(unexpected.join(QStringLiteral("\n")))));
//   }
//
// The guard chains to the previously installed handler (QtTest's), so QWARN
// output and counting keep working while the guard watches.
namespace UiTest {

class QmlWarningGuard
{
public:
    static QmlWarningGuard &instance();

    // Installs the watching handler (idempotent). Must run after QtTest
    // installed its own handler, i.e. from initTestCase, not main().
    void install();

    // Drops the messages collected so far. Call before each test function.
    void reset();

    // Messages collected since the last reset() that match the unexpected
    // patterns; empty means the test ran clean.
    QStringList unexpectedMessages() const;

private:
    QmlWarningGuard() = default;
    QStringList m_unexpected;
    QtMessageHandler m_previous = nullptr;
    bool m_installed = false;

    void recordUnexpected(const QString &message);
    void forwardMessage(QtMsgType type, const QMessageLogContext &context, const QString &message);

    friend void guardingMessageHandler(QtMsgType, const QMessageLogContext &, const QString &);
};

} // namespace UiTest
