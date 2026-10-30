#include "DesktopIntegration.h"

#include "core/CoreBackend.h"
#include "models/RoleListModel.h"

#include <QAction>
#include <QCoreApplication>
#include <QGuiApplication>
#include <QImage>
#include <QCoreApplication>
#include <QDir>
#include <QFile>
#include <QIcon>
#include <QTimer>
#include <QClipboard>
#include <QGuiApplication>
#include <QSettings>
#include <QStandardPaths>
#include <QMenu>
#include <QPalette>
#include <QPixmap>
#include <QSystemTrayIcon>
#include <QWindow>

namespace {

// Native menu rendering follows the OS light/dark setting instead of the
// application palette, which yields white-on-white items under the dark
// theme; a stylesheet forces Qt to draw the menu with palette colors.
QString menuStyleSheet(const QPalette &palette)
{
    const QColor window = palette.color(QPalette::Active, QPalette::Window);
    const QColor surface = palette.color(QPalette::Active, QPalette::AlternateBase);
    const QColor text = palette.color(QPalette::Active, QPalette::WindowText);
    const QColor muted = palette.color(QPalette::Active, QPalette::PlaceholderText);
    const QColor border = palette.color(QPalette::Active, QPalette::Mid);
    return QStringLiteral(
               "QMenu { background-color: %1; color: %2; border: 1px solid %3; }"
               "QMenu::item { padding: 6px 28px 6px 12px; background: transparent; }"
               "QMenu::item:selected { background-color: %4; }"
               "QMenu::item:disabled { color: %5; }"
               "QMenu::separator { height: 1px; background: %3; margin: 4px 8px; }")
        .arg(window.name(), text.name(), border.name(), surface.name(), muted.name());
}

QIcon trayIcon()
{
    // The packaged application icon is the tray icon; the drawn one is a fallback
    // for builds without the resource.
    const QIcon packaged(QStringLiteral(":/resources/kikimora.png"));
    if (!packaged.isNull()) return packaged;
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

// Role lines read as "● awg-main — Ready": a filled bullet while the role is
// carrying traffic keeps the tray scannable without opening the window.
QString roleLine(const RoleSnapshot &role)
{
    const QString name = role.label.isEmpty() ? role.id : role.label;
    const QString state = role.stateText.isEmpty() ? role.state : role.stateText;
    const bool up = role.state.compare(QStringLiteral("Ready"), Qt::CaseInsensitive) == 0
        || role.sessionConnected;
    return QStringLiteral("%1 %2 — %3").arg(up ? QStringLiteral("●") : QStringLiteral("○"), name, state);
}

} // namespace

namespace {

constexpr auto autostartName = "Kikimora";

QString autostartEntryPath()
{
#ifdef Q_OS_WIN
	return QStringLiteral("HKEY_CURRENT_USER\\Software\\Microsoft\\Windows\\CurrentVersion\\Run");
#else
	const QString config = QStandardPaths::writableLocation(QStandardPaths::GenericConfigLocation);
	return config + QStringLiteral("/autostart/kikimora.desktop");
#endif
}

#ifdef Q_OS_WIN
// The all-users Startup shortcut the MSI installs. When it exists it is the
// authoritative autostart: the per-user Run entry would fire a second
// instance at every logon (single-instance mutex makes that harmless, but
// the duplicate trigger is still removed for honesty).
QString allUsersStartupShortcut()
{
	return QStringLiteral("C:\\ProgramData\\Microsoft\\Windows\\Start Menu\\Programs\\StartUp\\Kikimora.lnk");
}

void removePerUserRunEntry()
{
	QSettings run(autostartEntryPath(), QSettings::NativeFormat);
	if (run.contains(QStringLiteral("Kikimora"))) {
		run.remove(QStringLiteral("Kikimora"));
		run.sync();
	}
}
#endif

} // namespace

DesktopIntegration::DesktopIntegration(CoreBackend *core, QObject *parent)
    : QObject(parent)
    , m_core(core)
    , m_trayAvailable(QSystemTrayIcon::isSystemTrayAvailable())
{
#ifdef Q_OS_WIN
    // One autostart trigger is enough: when the installer's all-users Startup
    // shortcut exists, the per-user Run entry left by an older toggle is a
    // duplicate launch at every logon. Portable installs have no shortcut —
    // their Run entry IS the autostart mechanism and must survive.
    if (QFile::exists(allUsersStartupShortcut()))
        removePerUserRunEntry();
#endif
    m_menu = new QMenu;
    m_menu->setStyleSheet(menuStyleSheet(QGuiApplication::palette()));
    m_stateAction = m_menu->addAction(QString());
    m_stateAction->setEnabled(false);
    m_connectAction = m_menu->addAction(tr("Connect all tunnels"));
    m_menu->addSeparator();
    m_showAction = m_menu->addAction(tr("Show Kikimora"));
    m_menu->addSeparator();
    m_quitAction = m_menu->addAction(tr("Quit"));

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

    ensureTray();
    if (!m_trayAvailable) {
        m_trayWatch = new QTimer(this);
        m_trayWatch->setInterval(1000);
        connect(m_trayWatch, &QTimer::timeout, this, [this] {
            if (QSystemTrayIcon::isSystemTrayAvailable()) {
                m_trayWatch->stop();
                ensureTray();
            }
        });
        m_trayWatch->start();
    }
    refreshMenu();
}

DesktopIntegration::~DesktopIntegration()
{
    delete m_menu;
}




bool DesktopIntegration::autostartEnabled() const
{
#ifdef Q_OS_WIN
	if (QFile::exists(allUsersStartupShortcut()))
		return true;
	QSettings run(autostartEntryPath(), QSettings::NativeFormat);
	return run.contains(QStringLiteral("Kikimora"));
#else
	return QFile::exists(autostartEntryPath());
#endif
}

QString DesktopIntegration::autostartTarget() const
{
#ifdef Q_OS_WIN
	QSettings run(autostartEntryPath(), QSettings::NativeFormat);
	return run.value(QStringLiteral("Kikimora")).toString();
#else
	return autostartEntryPath();
#endif
}

void DesktopIntegration::setAutostartEnabled(bool enabled)
{
	const QString executable = QCoreApplication::applicationFilePath();
#ifdef Q_OS_WIN
	if (!enabled && QFile::exists(allUsersStartupShortcut())) {
		// The installer owns the all-users Startup shortcut; a non-elevated
		// UI cannot remove it. Drop only the redundant per-user entry and
		// leave autostart reporting enabled (it is: the shortcut remains).
		removePerUserRunEntry();
		emit autostartChanged();
		return;
	}
	QSettings run(autostartEntryPath(), QSettings::NativeFormat);
	if (enabled)
		run.setValue(QStringLiteral("Kikimora"), QStringLiteral("\"%1\" --background").arg(QDir::toNativeSeparators(executable)));
	else
		run.remove(QStringLiteral("Kikimora"));
	run.sync();
#else
	const QString path = autostartEntryPath();
	if (enabled) {
		QDir().mkpath(QFileInfo(path).absolutePath());
		QFile file(path);
		if (file.open(QIODevice::WriteOnly | QIODevice::Truncate)) {
			const QString entry = QStringLiteral(
				"[Desktop Entry]\nType=Application\nName=Kikimora\nComment=Kikimora VPN client\n"
				"Exec=%1 --background\nIcon=kikimora\nTerminal=false\nX-GNOME-Autostart-enabled=true\n")
					.arg(executable);
			file.write(entry.toUtf8());
		}
	} else {
		QFile::remove(path);
	}
#endif
	emit autostartChanged();
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

QString DesktopIntegration::clipboardText() const
{
	const QClipboard *clipboard = QGuiApplication::clipboard();
	return clipboard ? clipboard->text() : QString();
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

void DesktopIntegration::rebuildRoleLines()
{
    for (QAction *action : m_roleActions) {
        m_menu->removeAction(action);
        action->deleteLater();
    }
    m_roleActions.clear();
    if (!m_core || !m_core->roles()) return;

    RoleListModel *roles = m_core->roles();
    for (int row = 0; row < roles->rowCount(); ++row) {
        auto *action = new QAction(roleLine(roles->roleAt(row)), m_menu);
        action->setEnabled(false);
        // Keep every role line above the window/quit block.
        m_menu->insertAction(m_showAction, action);
        m_roleActions.append(action);
    }
    // One separator for the whole lifetime of the menu: inserting a fresh one on every
    // snapshot made the tray menu grow without bound.
    if (!m_roleActions.isEmpty()) {
        if (!m_roleSeparator) {
            m_roleSeparator = new QAction(m_menu);
            m_roleSeparator->setSeparator(true);
        }
        m_menu->insertAction(m_showAction, m_roleSeparator);
        m_roleSeparator->setVisible(true);
    } else if (m_roleSeparator) {
        m_roleSeparator->setVisible(false);
    }
}

// ensureTray creates the tray icon once a host exists. It is called from the constructor and
// from the watch timer, because a session that starts before the shell is ready reports no
// tray host: without the retry the window would quit on close instead of hiding.
void DesktopIntegration::ensureTray()
{
	if (m_tray || !QSystemTrayIcon::isSystemTrayAvailable()) return;
	m_trayAvailable = true;
	m_tray = new QSystemTrayIcon(trayIcon(), this);
	m_tray->setToolTip(QStringLiteral("Kikimora"));
	m_tray->setContextMenu(m_menu);
	connect(m_tray, &QSystemTrayIcon::activated, this, [this](QSystemTrayIcon::ActivationReason reason) {
		if (reason == QSystemTrayIcon::Trigger || reason == QSystemTrayIcon::DoubleClick)
			showWindow();
	});
	m_tray->show();
	emit trayChanged();
}

void DesktopIntegration::refreshMenu()
{
    if (!m_core) return;
    m_menu->setStyleSheet(menuStyleSheet(QGuiApplication::palette()));
    const bool connected = m_core->coreState() != QStringLiteral("Unavailable") &&
                           m_core->coreState() != QStringLiteral("Error");
    const QString state = m_core->aggregateStateText();
    m_stateAction->setText(state.isEmpty() ? m_core->coreState() : state);
    m_connectAction->setText(m_core->aggregateActionText() == QStringLiteral("DISCONNECT")
                                 ? tr("Disconnect all tunnels")
                                 : tr("Connect all tunnels"));
    m_connectAction->setEnabled(connected);
    rebuildRoleLines();
}
