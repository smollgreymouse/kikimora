#pragma once

#include <QObject>
#include <QString>
#include <QVector>

class QAction;
class CoreBackend;
class QMenu;
class QSystemTrayIcon;
class QWindow;

// Desktop shell around the core connection: one window plus a tray menu that
// mirrors what the window shows. The menu is the only surface available when
// the window is hidden, so it carries the aggregate state, the all-roles
// toggle and one line per role — the same information Main.qml puts on the
// connect page.
class DesktopIntegration final : public QObject
{
    Q_OBJECT
    Q_PROPERTY(bool trayAvailable READ trayAvailable CONSTANT)
    Q_PROPERTY(bool quitting READ quitting NOTIFY quittingChanged)

public:
    explicit DesktopIntegration(CoreBackend *core, QObject *parent = nullptr);
    ~DesktopIntegration() override;

    bool trayAvailable() const { return m_trayAvailable; }
    bool quitting() const { return m_quitting; }

    // Exposed for the tray tests: the menu is otherwise owned by the tray icon.
    QMenu *contextMenu() const { return m_menu; }

    void setWindow(QWindow *window);

    Q_INVOKABLE void showWindow();
    Q_INVOKABLE void hideWindow();
    Q_INVOKABLE void quit();

signals:
    void quittingChanged();

private:
    void refreshMenu();
    void rebuildRoleLines();

    CoreBackend *m_core;
    QWindow *m_window = nullptr;
    QSystemTrayIcon *m_tray = nullptr;
    QMenu *m_menu = nullptr;
    QAction *m_showAction = nullptr;
    QAction *m_connectAction = nullptr;
    QAction *m_quitAction = nullptr;
    // Informational, never clickable: the aggregate state line and the
    // per-role state lines.
    QAction *m_stateAction = nullptr;
    QVector<QAction *> m_roleActions;
    bool m_trayAvailable = false;
    bool m_quitting = false;
};
