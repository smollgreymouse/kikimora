#include "DesktopIntegration.h"

#include "core/CoreBackend.h"

#include <QAction>
#include <QCoreApplication>
#include <QImage>
#include <QIcon>
#include <QMenu>
#include <QPixmap>
#include <QSystemTrayIcon>
#include <QWindow>

namespace {

QIcon trayIcon()
{
    QImage image(QStringLiteral(":/resources/kikimora.png"));
    if (image.isNull())
        return {};

    QRect content;
    for (int y = 0; y < image.height(); ++y) {
        for (int x = 0; x < image.width(); ++x) {
            if (qAlpha(image.pixel(x, y)) > 16)
                content |= QRect(x, y, 1, 1);
        }
    }
    if (!content.isEmpty()) {
        const int padding = qMax(1, qMin(content.width(), content.height()) / 24);
        content.adjust(-padding, -padding, padding, padding);
        content = content.intersected(image.rect());
        image = image.copy(content);
    }
    return QIcon(QPixmap::fromImage(image));
}

} // namespace

DesktopIntegration::DesktopIntegration(CoreBackend *core, QObject *parent)
    : QObject(parent)
    , m_core(core)
    , m_trayAvailable(QSystemTrayIcon::isSystemTrayAvailable())
{
    m_menu = new QMenu;
    m_showAction = m_menu->addAction(QStringLiteral("Show Kikimora"));
    m_connectAction = m_menu->addAction(QStringLiteral("Connect all tunnels"));
    m_menu->addSeparator();
    m_quitAction = m_menu->addAction(QStringLiteral("Quit"));

    connect(m_showAction, &QAction::triggered, this, &DesktopIntegration::showWindow);
    connect(m_connectAction, &QAction::triggered, this, [this] {
        if (!m_core) return;
        if (m_core->aggregateActionText() == QStringLiteral("DISCONNECT"))
            m_core->disconnectAll();
        else
            m_core->connectAll();
    });
    connect(m_quitAction, &QAction::triggered, this, &DesktopIntegration::quit);
    if (m_core) connect(m_core, &CoreBackend::snapshotChanged, this, &DesktopIntegration::refreshMenu);

    m_tray = new QSystemTrayIcon(trayIcon(), this);
    m_tray->setToolTip(QStringLiteral("Kikimora"));
    m_tray->setContextMenu(m_menu);
    connect(m_tray, &QSystemTrayIcon::activated, this, [this](QSystemTrayIcon::ActivationReason reason) {
        if (reason == QSystemTrayIcon::Trigger || reason == QSystemTrayIcon::DoubleClick)
            showWindow();
    });
    if (m_trayAvailable) m_tray->show();
    refreshMenu();
}

DesktopIntegration::~DesktopIntegration()
{
    delete m_menu;
}

void DesktopIntegration::setWindow(QWindow *window)
{
    m_window = window;
}

void DesktopIntegration::showWindow()
{
    if (!m_window) return;
    m_window->showNormal();
    m_window->raise();
    m_window->requestActivate();
}

void DesktopIntegration::hideWindow()
{
    if (m_window) m_window->hide();
}

void DesktopIntegration::quit()
{
    if (!m_quitting) {
        m_quitting = true;
        emit quittingChanged();
    }
    if (m_tray) m_tray->hide();
    QCoreApplication::quit();
}

void DesktopIntegration::refreshMenu()
{
    if (!m_core) return;
    const bool connected = m_core->coreState() != QStringLiteral("Unavailable") &&
                           m_core->coreState() != QStringLiteral("Error");
    m_connectAction->setText(m_core->aggregateActionText() == QStringLiteral("DISCONNECT")
                                 ? QStringLiteral("Disconnect all tunnels")
                                 : QStringLiteral("Connect all tunnels"));
    m_connectAction->setEnabled(connected);
}
