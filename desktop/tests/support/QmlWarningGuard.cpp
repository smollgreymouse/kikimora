#include "QmlWarningGuard.h"

#include <QLoggingCategory>
#include <QRegularExpression>

namespace UiTest {

namespace {

// Messages that mean a real test/product problem. Each pattern is a failure;
// nothing below is filtered out silently.
const QRegularExpression *const kUnexpectedPatterns[] = {
    // JS/QML runtime errors.
    new QRegularExpression(QStringLiteral("ReferenceError")),
    new QRegularExpression(QStringLiteral("TypeError")),
    new QRegularExpression(QStringLiteral("is not defined")),
    new QRegularExpression(QStringLiteral("is not a function")),
    // QML engine problems.
    new QRegularExpression(QStringLiteral("Binding loop")),
    new QRegularExpression(QStringLiteral("QQmlApplicationEngine failed")),
    new QRegularExpression(QStringLiteral("required property")),
    new QRegularExpression(QStringLiteral("Cannot assign")),
    new QRegularExpression(QStringLiteral("Unable to assign")),
    new QRegularExpression(QStringLiteral("qrc:/qml/.+:.+:.+")),
    // Bootstrap ordering: the production pin must be applied before the
    // first Controls import of the process.
    new QRegularExpression(QStringLiteral("QQuickStyle::setStyle\\(\\) must be called")),
};

bool matchesUnexpected(const QString &message)
{
    for (const QRegularExpression *pattern : kUnexpectedPatterns) {
        if (message.contains(*pattern))
            return true;
    }
    return false;
}

} // namespace

// Defined at UiTest scope so the class's friend declaration matches.
void guardingMessageHandler(QtMsgType type, const QMessageLogContext &context, const QString &message)
{
    // Only warnings and errors can indicate a runtime problem; debug/info
    // lines stay pure logging.
    if (type == QtWarningMsg || type == QtCriticalMsg || type == QtFatalMsg) {
        if (matchesUnexpected(message))
            QmlWarningGuard::instance().recordUnexpected(message);
    }
    // Chain to the previously installed handler so QtTest keeps printing
    // QWARN lines and its own warning bookkeeping keeps working.
    QmlWarningGuard::instance().forwardMessage(type, context, message);
}

QmlWarningGuard &QmlWarningGuard::instance()
{
    static QmlWarningGuard guard;
    return guard;
}

void QmlWarningGuard::install()
{
    if (m_installed)
        return;
    m_installed = true;
    m_previous = qInstallMessageHandler(guardingMessageHandler);
}

void QmlWarningGuard::reset()
{
    m_unexpected.clear();
}

QStringList QmlWarningGuard::unexpectedMessages() const
{
    return m_unexpected;
}

void QmlWarningGuard::recordUnexpected(const QString &message)
{
    if (!m_unexpected.contains(message))
        m_unexpected.append(message);
}

void QmlWarningGuard::forwardMessage(QtMsgType type, const QMessageLogContext &context, const QString &message)
{
    if (m_previous)
        m_previous(type, context, message);
}

} // namespace UiTest
