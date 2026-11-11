#include "UiTestFixture.h"

#include <QQuickStyle>

namespace UiTest {

void ensureProductionBootstrap()
{
    // Idempotent per process: the first call pins the style the way
    // production main.cpp does, later calls must not re-apply it (the
    // re-application itself prints an ordering warning).
    static const bool applied = [] {
        QQuickStyle::setStyle(QStringLiteral("Basic"));
        return true;
    }();
    Q_UNUSED(applied);
}

} // namespace UiTest
