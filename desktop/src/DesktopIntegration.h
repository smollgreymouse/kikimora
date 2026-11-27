#pragma once

#include <QIcon>
#include <QObject>
#include <QString>
#include <QVector>

class QAction;
class CoreBackend;
class QMenu;
class QProcess;
class QSystemTrayIcon;
class QImage;
class QWindow;

// Desktop shell around the core connection: one window plus a tray menu that
// mirrors what the window shows. The menu is the only surface available when
// the window is hidden, so it carries the aggregate state, the all-roles
// toggle and one line per role — the same information Main.qml puts on the
// connect page.
class QTimer;

class DesktopIntegration final : public QObject
{
    Q_OBJECT
    Q_PROPERTY(bool trayAvailable READ trayAvailable NOTIFY trayChanged)
    Q_PROPERTY(bool quitting READ quitting NOTIFY quittingChanged)
    // Start with the session. Windows uses the per-user Run key, Linux an XDG autostart
    // entry, so no elevation is needed on either platform.
    Q_PROPERTY(bool autostartEnabled READ autostartEnabled NOTIFY autostartChanged)
    Q_PROPERTY(QString autostartTarget READ autostartTarget NOTIFY autostartChanged)
    // The first-run authorization transaction (09G-W1.5): one pkexec helper
    // run, one final signal per attempt.
    Q_PROPERTY(bool initializationPending READ initializationPending NOTIFY initializationChanged)
    Q_PROPERTY(QString initializationError READ initializationError NOTIFY initializationChanged)

public:
    explicit DesktopIntegration(CoreBackend *core, QObject *parent = nullptr);
    ~DesktopIntegration() override;

    bool trayAvailable() const { return m_trayAvailable; }
    bool quitting() const { return m_quitting; }

    // Exposed for the tray tests: the menu is otherwise owned by the tray icon.
    QMenu *contextMenu() const { return m_menu; }

    // Fallback tray rendering for builds without the packaged resource. Public
    // so the crop contract is testable without a missing resource.
    static QIcon trayIconForImage(const QImage &image);

    void setWindow(QWindow *window);

    Q_INVOKABLE void showWindow();
    Q_INVOKABLE void hideWindow();
    Q_INVOKABLE void quit();
    // The import field can paste a share link or a JSON config.
    Q_INVOKABLE QString clipboardText() const;
    bool autostartEnabled() const;
    QString autostartTarget() const;
    Q_INVOKABLE void setAutostartEnabled(bool enabled);
    bool initializationPending() const { return m_initializationPending; }
    QString initializationError() const { return m_initializationError; }
    // One pkexec run of the fixed helper; completes asynchronously through
    // initializationChanged exactly once per attempt.
    Q_INVOKABLE void initializeKikimora();

signals:
    void quittingChanged();
    // Emitted when the tray host appears after the window was already up.
    void trayChanged();
    void autostartChanged();
    void initializationChanged();

private:
    void ensureTray();
    void refreshMenu();
    void rebuildRoleLines();
    void finishInitialization(const QString &error);

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
    QAction *m_roleSeparator = nullptr;
    bool m_trayAvailable = false;
    QTimer *m_trayWatch = nullptr;
    bool m_quitting = false;
    QProcess *m_initializationProcess = nullptr;
    bool m_initializationPending = false;
    QString m_initializationError;
};
