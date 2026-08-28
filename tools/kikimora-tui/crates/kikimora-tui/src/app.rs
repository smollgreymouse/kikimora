use crossterm::event::{KeyCode, KeyEvent, MouseButton, MouseEvent, MouseEventKind};
use kikimora_core::{
    Backend, BackendError, DnsResponse, EndpointsResponse, LogsResponse, PlatformBackend,
    ProfilesResponse, ServiceAction, StatusResponse,
};
use ratatui::layout::Rect;

use crate::settings::LocalSettings;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Tab {
    Status,
    Profiles,
    Endpoints,
    Dns,
    Logs,
    Settings,
}

pub const TABS: [Tab; 6] = [
    Tab::Status,
    Tab::Profiles,
    Tab::Endpoints,
    Tab::Dns,
    Tab::Logs,
    Tab::Settings,
];

impl Tab {
    pub const fn title(self) -> &'static str {
        match self {
            Self::Status => "Status",
            Self::Profiles => "Profiles",
            Self::Endpoints => "Endpoints",
            Self::Dns => "DNS",
            Self::Logs => "Logs",
            Self::Settings => "Settings",
        }
    }

    pub fn index(self) -> usize {
        TABS.iter().position(|tab| *tab == self).unwrap_or_default()
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum EndpointRoleChoice {
    Primary,
    Secondary,
}

impl EndpointRoleChoice {
    pub const fn key(self) -> &'static str {
        match self {
            Self::Primary => "primary",
            Self::Secondary => "secondary",
        }
    }

    pub const fn title(self) -> &'static str {
        match self {
            Self::Primary => "Primary",
            Self::Secondary => "Secondary",
        }
    }

    pub const fn toggle(self) -> Self {
        match self {
            Self::Primary => Self::Secondary,
            Self::Secondary => Self::Primary,
        }
    }
}

#[derive(Clone, Debug)]
pub enum HitTarget {
    Tab(Tab),
    Service(ServiceAction),
    Profile(usize),
    EndpointRole(EndpointRoleChoice),
    Rediscover,
    Invalidate,
    Dns(bool),
    ToggleStartup,
    ToggleEndpointManagement,
    CycleProfile,
}

#[derive(Clone, Debug)]
pub struct HitBox {
    pub rect: Rect,
    pub target: HitTarget,
}

pub struct App {
    pub backend: PlatformBackend,
    pub tab: Tab,
    pub status: Option<StatusResponse>,
    pub profiles: Option<ProfilesResponse>,
    pub endpoints: Option<EndpointsResponse>,
    pub dns: Option<DnsResponse>,
    pub logs: Option<LogsResponse>,
    pub selected_profile: usize,
    pub endpoint_role: EndpointRoleChoice,
    pub log_scroll: u16,
    pub message: String,
    pub quit: bool,
    pub local_settings: LocalSettings,
    pub hitboxes: Vec<HitBox>,
}

impl App {
    pub fn new(backend: PlatformBackend) -> Self {
        Self {
            backend,
            tab: Tab::Status,
            status: None,
            profiles: None,
            endpoints: None,
            dns: None,
            logs: None,
            selected_profile: 0,
            endpoint_role: EndpointRoleChoice::Secondary,
            log_scroll: 0,
            message: "Loading Kikimora state…".to_owned(),
            quit: false,
            local_settings: LocalSettings::load(),
            hitboxes: Vec::new(),
        }
    }

    pub fn register_hitbox(&mut self, rect: Rect, target: HitTarget) {
        if rect.width > 0 && rect.height > 0 {
            self.hitboxes.push(HitBox { rect, target });
        }
    }

    pub async fn refresh_all(&mut self) {
        let (status, profiles, endpoints, dns, logs) = tokio::join!(
            self.backend.status(),
            self.backend.profiles(),
            self.backend.endpoints(),
            self.backend.dns(),
            self.backend.logs(200),
        );

        let mut errors = Vec::new();
        match status {
            Ok(value) => self.status = Some(value),
            Err(error) => errors.push(error.to_string()),
        }
        match profiles {
            Ok(value) => {
                self.selected_profile = value
                    .active
                    .as_ref()
                    .and_then(|active| value.profiles.iter().position(|item| &item.name == active))
                    .unwrap_or(0);
                self.profiles = Some(value);
            }
            Err(error) => errors.push(error.to_string()),
        }
        match endpoints {
            Ok(value) => self.endpoints = Some(value),
            Err(error) => errors.push(error.to_string()),
        }
        match dns {
            Ok(value) => self.dns = Some(value),
            Err(error) => errors.push(error.to_string()),
        }
        match logs {
            Ok(value) => self.logs = Some(value),
            Err(error) => errors.push(error.to_string()),
        }

        self.message = if errors.is_empty() {
            "State refreshed".to_owned()
        } else {
            errors.join(" | ")
        };
    }

    pub async fn handle_key(&mut self, key: KeyEvent) {
        match key.code {
            KeyCode::Char('q') | KeyCode::Esc => self.quit = true,
            KeyCode::Char('1') => self.tab = Tab::Status,
            KeyCode::Char('2') => self.tab = Tab::Profiles,
            KeyCode::Char('3') => self.tab = Tab::Endpoints,
            KeyCode::Char('4') => self.tab = Tab::Dns,
            KeyCode::Char('5') => self.tab = Tab::Logs,
            KeyCode::Char('6') => self.tab = Tab::Settings,
            KeyCode::Tab | KeyCode::Right => self.next_tab(),
            KeyCode::BackTab | KeyCode::Left => self.previous_tab(),
            KeyCode::F(5) => self.refresh_all().await,
            KeyCode::Up => self.move_selection(-1),
            KeyCode::Down => self.move_selection(1),
            KeyCode::PageUp if self.tab == Tab::Logs => {
                self.log_scroll = self.log_scroll.saturating_sub(10)
            }
            KeyCode::PageDown if self.tab == Tab::Logs => {
                self.log_scroll = self.log_scroll.saturating_add(10)
            }
            KeyCode::Enter if matches!(self.tab, Tab::Status | Tab::Profiles) => {
                self.use_selected_profile().await
            }
            KeyCode::Char('s') if self.tab == Tab::Status => {
                self.run_service(ServiceAction::Start).await
            }
            KeyCode::Char('x') if self.tab == Tab::Status => {
                self.run_service(ServiceAction::Stop).await
            }
            KeyCode::Char('r') if self.tab == Tab::Status => {
                self.run_service(ServiceAction::Restart).await
            }
            KeyCode::Char('d') if self.tab == Tab::Endpoints => self.rediscover().await,
            KeyCode::Char('i') if self.tab == Tab::Endpoints => self.invalidate().await,
            KeyCode::Char('m') if self.tab == Tab::Settings => self.toggle_endpoint_management(),
            KeyCode::Char('p') if self.tab == Tab::Settings => self.cycle_profile().await,
            KeyCode::Char('d') if self.tab == Tab::Settings => self.toggle_dns().await,
            KeyCode::Char('a') if self.tab == Tab::Settings => self.toggle_startup().await,
            _ => {}
        }
    }

    pub async fn handle_mouse(&mut self, mouse: MouseEvent) {
        match mouse.kind {
            MouseEventKind::Down(MouseButton::Left) => {
                let target = self
                    .hitboxes
                    .iter()
                    .rev()
                    .find(|hitbox| contains(hitbox.rect, mouse.column, mouse.row))
                    .map(|hitbox| hitbox.target.clone());
                if let Some(target) = target {
                    self.activate(target).await;
                }
            }
            MouseEventKind::ScrollUp if self.tab == Tab::Logs => {
                self.log_scroll = self.log_scroll.saturating_sub(3)
            }
            MouseEventKind::ScrollDown if self.tab == Tab::Logs => {
                self.log_scroll = self.log_scroll.saturating_add(3)
            }
            _ => {}
        }
    }

    fn next_tab(&mut self) {
        self.tab = TABS[(self.tab.index() + 1) % TABS.len()];
    }

    fn previous_tab(&mut self) {
        self.tab = TABS[(self.tab.index() + TABS.len() - 1) % TABS.len()];
    }

    fn move_selection(&mut self, direction: isize) {
        match self.tab {
            Tab::Status | Tab::Profiles => {
                let len = self
                    .profiles
                    .as_ref()
                    .map(|profiles| profiles.profiles.len())
                    .unwrap_or_default();
                if len == 0 {
                    return;
                }
                let next = (self.selected_profile as isize + direction)
                    .clamp(0, len.saturating_sub(1) as isize);
                self.selected_profile = next as usize;
            }
            Tab::Endpoints => self.endpoint_role = self.endpoint_role.toggle(),
            _ => {}
        }
    }

    async fn activate(&mut self, target: HitTarget) {
        match target {
            HitTarget::Tab(tab) => self.tab = tab,
            HitTarget::Service(action) => self.run_service(action).await,
            HitTarget::Profile(index) => {
                self.selected_profile = index;
                self.use_selected_profile().await;
            }
            HitTarget::EndpointRole(role) => self.endpoint_role = role,
            HitTarget::Rediscover => self.rediscover().await,
            HitTarget::Invalidate => self.invalidate().await,
            HitTarget::Dns(use_leshy) => self.set_dns(use_leshy).await,
            HitTarget::ToggleStartup => self.toggle_startup().await,
            HitTarget::ToggleEndpointManagement => self.toggle_endpoint_management(),
            HitTarget::CycleProfile => self.cycle_profile().await,
        }
    }

    async fn run_service(&mut self, action: ServiceAction) {
        let result = self.backend.service(action).await;
        self.finish_mutation(result, format!("Service action: {}", action.as_cli_arg()))
            .await;
    }

    async fn use_selected_profile(&mut self) {
        let Some(profile) = self
            .profiles
            .as_ref()
            .and_then(|profiles| profiles.profiles.get(self.selected_profile))
        else {
            return;
        };
        let name = profile.name.clone();
        let result = self.backend.use_profile(&name).await;
        self.finish_mutation(result, format!("Profile selected: {name}"))
            .await;
    }

    async fn cycle_profile(&mut self) {
        let Some(profiles) = self.profiles.as_ref() else {
            return;
        };
        if profiles.profiles.is_empty() {
            return;
        }
        self.selected_profile = (self.selected_profile + 1) % profiles.profiles.len();
        self.use_selected_profile().await;
    }

    fn endpoint_action_supported(&self, invalidate: bool) -> bool {
        self.endpoints
            .as_ref()
            .map(|endpoints| match self.endpoint_role {
                EndpointRoleChoice::Primary => &endpoints.roles.primary.actions,
                EndpointRoleChoice::Secondary => &endpoints.roles.secondary.actions,
            })
            .map(|actions| {
                if invalidate {
                    actions.invalidate
                } else {
                    actions.rediscover
                }
            })
            .unwrap_or(false)
    }

    async fn rediscover(&mut self) {
        if !self.local_settings.manage_vpn_endpoints {
            self.message = "Endpoint management is disabled in TUI settings".to_owned();
            return;
        }
        if !self.endpoint_action_supported(false) {
            self.message = "Endpoint rediscovery is not supported for the selected role".to_owned();
            return;
        }
        let role = self.endpoint_role.key();
        let result = self.backend.rediscover_endpoints(role).await;
        self.finish_mutation(result, format!("Endpoint rediscovery requested: {role}"))
            .await;
    }

    async fn invalidate(&mut self) {
        if !self.local_settings.manage_vpn_endpoints {
            self.message = "Endpoint management is disabled in TUI settings".to_owned();
            return;
        }
        if !self.endpoint_action_supported(true) {
            self.message = "Endpoint invalidation is not supported for the selected role".to_owned();
            return;
        }
        let role = self.endpoint_role.key();
        let result = self.backend.invalidate_endpoint_cache(role).await;
        self.finish_mutation(result, format!("Endpoint cache invalidated: {role}"))
            .await;
    }

    async fn set_dns(&mut self, use_leshy: bool) {
        let result = self.backend.set_dns(use_leshy).await;
        self.finish_mutation(
            result,
            format!("DNS selected: {}", if use_leshy { "Leshy" } else { "System" }),
        )
        .await;
    }

    async fn toggle_dns(&mut self) {
        let use_leshy = self
            .dns
            .as_ref()
            .map(|dns| dns.provider != "leshy")
            .unwrap_or(true);
        self.set_dns(use_leshy).await;
    }

    async fn toggle_startup(&mut self) {
        let enabled = !self
            .status
            .as_ref()
            .map(|status| status.startup.enabled)
            .unwrap_or(false);
        let result = self.backend.set_startup(enabled).await;
        self.finish_mutation(
            result,
            format!("Autostart {}", if enabled { "enabled" } else { "disabled" }),
        )
        .await;
    }

    fn toggle_endpoint_management(&mut self) {
        self.local_settings.manage_vpn_endpoints = !self.local_settings.manage_vpn_endpoints;
        self.message = match self.local_settings.save() {
            Ok(()) => format!(
                "TUI endpoint controls {}",
                if self.local_settings.manage_vpn_endpoints {
                    "enabled"
                } else {
                    "disabled"
                }
            ),
            Err(error) => format!("Could not save TUI settings: {error}"),
        };
    }

    async fn finish_mutation(&mut self, result: Result<(), BackendError>, success: String) {
        match result {
            Ok(()) => {
                self.message = success;
                self.refresh_all().await;
            }
            Err(error) => self.message = error.to_string(),
        }
    }
}

fn contains(rect: Rect, x: u16, y: u16) -> bool {
    x >= rect.x
        && x < rect.x.saturating_add(rect.width)
        && y >= rect.y
        && y < rect.y.saturating_add(rect.height)
}
