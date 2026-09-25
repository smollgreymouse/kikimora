#pragma once

#include <QObject>

class QAction;
class CoreBackend;
class QMenu;
class QSystemTrayIcon;
class QWindow;

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

    void setWindow(QWindow *window);

    Q_INVOKABLE void showWindow();
    Q_INVOKABLE void hideWindow();
    Q_INVOKABLE void quit();

signals:
    void quittingChanged();

private:
    void refreshMenu();

    CoreBackend *m_core;
    QWindow *m_window = nullptr;
    QSystemTrayIcon *m_tray = nullptr;
    QMenu *m_menu = nullptr;
    QAction *m_showAction = nullptr;
    QAction *m_connectAction = nullptr;
    QAction *m_quitAction = nullptr;
    bool m_trayAvailable = false;
    bool m_quitting = false;
};
