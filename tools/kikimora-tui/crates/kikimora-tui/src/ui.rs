use kikimora_core::{EndpointRole, ServiceAction};
use ratatui::layout::{Alignment, Constraint, Direction, Layout, Rect};
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, Borders, Cell, List, ListItem, Paragraph, Row, Table, Tabs, Wrap};
use ratatui::Frame;

use crate::app::{App, EndpointRoleChoice, HitTarget, Tab, TABS};

pub fn render(frame: &mut Frame<'_>, app: &mut App) {
    app.hitboxes.clear();
    let root = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(3), Constraint::Min(5), Constraint::Length(2)])
        .split(frame.area());

    render_tabs(frame, app, root[0]);
    match app.tab {
        Tab::Status => render_status(frame, app, root[1]),
        Tab::Profiles => render_profiles(frame, app, root[1]),
        Tab::Endpoints => render_endpoints(frame, app, root[1]),
        Tab::Dns => render_dns(frame, app, root[1]),
        Tab::Logs => render_logs(frame, app, root[1]),
        Tab::Settings => render_settings(frame, app, root[1]),
    }
    render_footer(frame, app, root[2]);
}

fn render_tabs(frame: &mut Frame<'_>, app: &mut App, area: Rect) {
    let titles = TABS
        .iter()
        .map(|tab| Line::from(tab.title()))
        .collect::<Vec<_>>();
    let tabs = Tabs::new(titles)
        .block(Block::default().title(" Kikimora ").borders(Borders::ALL))
        .select(app.tab.index())
        .highlight_style(Style::default().add_modifier(Modifier::REVERSED));
    frame.render_widget(tabs, area);

    let inner = Rect::new(
        area.x.saturating_add(1),
        area.y.saturating_add(1),
        area.width.saturating_sub(2),
        1,
    );
    let tab_areas = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Ratio(1, 6); 6])
        .split(inner);
    for (index, tab) in TABS.iter().enumerate() {
        app.register_hitbox(tab_areas[index], HitTarget::Tab(*tab));
    }
}

fn render_status(frame: &mut Frame<'_>, app: &mut App, area: Rect) {
    let profile_count = app
        .profiles
        .as_ref()
        .map(|profiles| profiles.profiles.len())
        .unwrap_or_default();
    let profile_height = (profile_count as u16 + 2).clamp(4, 9);
    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(profile_height),
            Constraint::Length(7),
            Constraint::Length(3),
            Constraint::Min(0),
        ])
        .split(area);

    render_profile_list(frame, app, chunks[0], " Profile ");

    let mut rows = Vec::new();
    if let Some(status) = &app.status {
        rows.push(Row::new(vec![
            Cell::from("Primary"),
            Cell::from(status.interfaces.primary.name.clone()),
            Cell::from(status.interfaces.primary.state.to_uppercase()),
        ]));
        rows.push(Row::new(vec![
            Cell::from("Secondary"),
            Cell::from(status.interfaces.secondary.name.clone()),
            Cell::from(status.interfaces.secondary.state.to_uppercase()),
        ]));
        rows.push(Row::new(vec![
            Cell::from("DNS"),
            Cell::from(status.dns.provider.clone()),
            Cell::from(status.interfaces.dns.state.to_uppercase()),
        ]));
        rows.push(Row::new(vec![
            Cell::from("Service"),
            Cell::from("leshy.service"),
            Cell::from(status.service.to_uppercase()),
        ]));
    } else {
        rows.push(Row::new(vec!["State", "", "loading…"]));
    }
    let table = Table::new(
        rows,
        [Constraint::Length(12), Constraint::Length(20), Constraint::Min(10)],
    )
    .header(Row::new(vec!["Role", "Name", "State"]).style(Style::default().add_modifier(Modifier::BOLD)))
    .block(Block::default().title(" Runtime ").borders(Borders::ALL));
    frame.render_widget(table, chunks[1]);

    render_service_buttons(frame, app, chunks[2]);
}

fn render_profile_list(frame: &mut Frame<'_>, app: &mut App, area: Rect, title: &'static str) {
    let items = app
        .profiles
        .as_ref()
        .map(|profiles| {
            profiles
                .profiles
                .iter()
                .enumerate()
                .map(|(index, profile)| {
                    let radio = if profile.active { "(*)" } else { "( )" };
                    let mut item = ListItem::new(format!("{radio} {}", profile.name));
                    if index == app.selected_profile {
                        item = item.style(Style::default().add_modifier(Modifier::REVERSED));
                    }
                    item
                })
                .collect::<Vec<_>>()
        })
        .unwrap_or_else(|| vec![ListItem::new("loading…")]);
    frame.render_widget(
        List::new(items).block(Block::default().title(title).borders(Borders::ALL)),
        area,
    );

    if let Some(profiles) = &app.profiles {
        let visible = area.height.saturating_sub(2) as usize;
        for index in 0..profiles.profiles.len().min(visible) {
            app.register_hitbox(
                Rect::new(
                    area.x.saturating_add(1),
                    area.y.saturating_add(1 + index as u16),
                    area.width.saturating_sub(2),
                    1,
                ),
                HitTarget::Profile(index),
            );
        }
    }
}

fn render_service_buttons(frame: &mut Frame<'_>, app: &mut App, area: Rect) {
    let parts = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Ratio(1, 3); 3])
        .split(area);
    let buttons = [
        ("Start", ServiceAction::Start),
        ("Stop", ServiceAction::Stop),
        ("Restart", ServiceAction::Restart),
    ];
    for (index, (label, action)) in buttons.into_iter().enumerate() {
        frame.render_widget(button(label), parts[index]);
        app.register_hitbox(parts[index], HitTarget::Service(action));
    }
}

fn render_profiles(frame: &mut Frame<'_>, app: &mut App, area: Rect) {
    let rows = app
        .profiles
        .as_ref()
        .map(|profiles| {
            profiles
                .profiles
                .iter()
                .enumerate()
                .map(|(index, profile)| {
                    let marker = if profile.active { "*" } else { " " };
                    let mut row = Row::new(vec![
                        marker.to_owned(),
                        profile.name.clone(),
                        profile.primary.interface.clone(),
                        profile.primary.endpoint_provider.clone(),
                        profile.secondary.interface.clone(),
                        profile.secondary.endpoint_provider.clone(),
                    ]);
                    if index == app.selected_profile {
                        row = row.style(Style::default().add_modifier(Modifier::REVERSED));
                    }
                    row
                })
                .collect::<Vec<_>>()
        })
        .unwrap_or_default();
    let table = Table::new(
        rows,
        [
            Constraint::Length(2),
            Constraint::Length(18),
            Constraint::Length(14),
            Constraint::Length(14),
            Constraint::Length(14),
            Constraint::Min(14),
        ],
    )
    .header(
        Row::new(vec!["", "Profile", "Primary", "P.Provider", "Secondary", "S.Provider"])
            .style(Style::default().add_modifier(Modifier::BOLD)),
    )
    .block(Block::default().title(" VPN profiles ").borders(Borders::ALL));
    frame.render_widget(table, area);

    if let Some(profiles) = &app.profiles {
        let visible = area.height.saturating_sub(3) as usize;
        for index in 0..profiles.profiles.len().min(visible) {
            app.register_hitbox(
                Rect::new(
                    area.x.saturating_add(1),
                    area.y.saturating_add(2 + index as u16),
                    area.width.saturating_sub(2),
                    1,
                ),
                HitTarget::Profile(index),
            );
        }
    }
}

fn render_endpoints(frame: &mut Frame<'_>, app: &mut App, area: Rect) {
    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(3), Constraint::Min(6), Constraint::Length(3)])
        .split(area);
    let role_areas = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Ratio(1, 2); 2])
        .split(chunks[0]);
    for (index, role) in [EndpointRoleChoice::Primary, EndpointRoleChoice::Secondary]
        .into_iter()
        .enumerate()
    {
        let label = if app.endpoint_role == role {
            format!("[{}]", role.title())
        } else {
            role.title().to_owned()
        };
        frame.render_widget(button(&label), role_areas[index]);
        app.register_hitbox(role_areas[index], HitTarget::EndpointRole(role));
    }

    let endpoint = selected_endpoint(app);
    frame.render_widget(
        Paragraph::new(endpoint_lines(endpoint))
            .block(Block::default().title(" Endpoint routing ").borders(Borders::ALL))
            .wrap(Wrap { trim: false }),
        chunks[1],
    );

    let action_areas = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Ratio(1, 2); 2])
        .split(chunks[2]);
    let management_enabled = app.local_settings.manage_vpn_endpoints;
    let can_rediscover = management_enabled
        && endpoint
            .map(|value| value.actions.rediscover)
            .unwrap_or(false);
    let can_invalidate = management_enabled
        && endpoint
            .map(|value| value.actions.invalidate)
            .unwrap_or(false);

    frame.render_widget(
        button(if can_rediscover {
            "Rediscover"
        } else {
            "Rediscover (disabled)"
        }),
        action_areas[0],
    );
    frame.render_widget(
        button(if can_invalidate {
            "Invalidate"
        } else {
            "Invalidate (unsupported)"
        }),
        action_areas[1],
    );
    if can_rediscover {
        app.register_hitbox(action_areas[0], HitTarget::Rediscover);
    }
    if can_invalidate {
        app.register_hitbox(action_areas[1], HitTarget::Invalidate);
    }
}

fn selected_endpoint(app: &App) -> Option<&EndpointRole> {
    app.endpoints.as_ref().map(|endpoints| match app.endpoint_role {
        EndpointRoleChoice::Primary => &endpoints.roles.primary,
        EndpointRoleChoice::Secondary => &endpoints.roles.secondary,
    })
}

fn endpoint_lines(endpoint: Option<&EndpointRole>) -> Vec<Line<'static>> {
    let Some(endpoint) = endpoint else {
        return vec![Line::from("loading…")];
    };

    let mut lines = vec![
        Line::from(vec![Span::raw("Provider: "), Span::raw(endpoint.provider.clone())]),
        Line::from(vec![Span::raw("Interface: "), Span::raw(endpoint.interface.clone())]),
        Line::from(vec![Span::raw("State: "), Span::raw(endpoint.state.clone())]),
        Line::from(vec![
            Span::raw("Pending: "),
            Span::raw(if endpoint.pending { "yes" } else { "no" }),
        ]),
    ];
    if !endpoint.provider_args.is_empty() {
        lines.push(Line::from(vec![
            Span::raw("Provider args: "),
            Span::raw(endpoint.provider_args.clone()),
        ]));
    }

    lines.push(Line::from(""));
    lines.push(Line::from(Span::styled(
        "Installed policy endpoints:",
        Style::default().add_modifier(Modifier::BOLD),
    )));
    if endpoint.installed.is_empty() {
        lines.push(Line::from("  —"));
    } else {
        for value in &endpoint.installed {
            lines.push(Line::from(format!("  {value}")));
        }
    }

    lines.push(Line::from(""));
    lines.push(Line::from(Span::styled(
        "Configured endpoint specs:",
        Style::default().add_modifier(Modifier::BOLD),
    )));
    if endpoint.configured.is_empty() {
        lines.push(Line::from("  —"));
    } else {
        for value in &endpoint.configured {
            lines.push(Line::from(format!("  {value}")));
        }
    }

    lines
}

fn render_dns(frame: &mut Frame<'_>, app: &mut App, area: Rect) {
    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Min(5), Constraint::Length(3)])
        .split(area);
    let text = if let Some(dns) = &app.dns {
        vec![
            Line::from(format!("Provider: {}", dns.provider)),
            Line::from(format!("Interface: {} ({})", dns.interface.name, dns.interface.state)),
            Line::from(format!("Listener: {}", dns.listen)),
            Line::from(format!("Leshy service: {}", dns.service)),
        ]
    } else {
        vec![Line::from("loading…")]
    };
    frame.render_widget(
        Paragraph::new(text).block(Block::default().title(" DNS ").borders(Borders::ALL)),
        chunks[0],
    );
    let choices = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Ratio(1, 2); 2])
        .split(chunks[1]);
    let leshy_active = app.dns.as_ref().map(|dns| dns.provider == "leshy").unwrap_or(false);
    frame.render_widget(button(if leshy_active { "(*) Leshy" } else { "( ) Leshy" }), choices[0]);
    frame.render_widget(button(if leshy_active { "( ) System" } else { "(*) System" }), choices[1]);
    app.register_hitbox(choices[0], HitTarget::Dns(true));
    app.register_hitbox(choices[1], HitTarget::Dns(false));
}

fn render_logs(frame: &mut Frame<'_>, app: &mut App, area: Rect) {
    let lines = app
        .logs
        .as_ref()
        .map(|logs| {
            logs.entries
                .iter()
                .map(|entry| {
                    let unit = entry
                        .get("_SYSTEMD_UNIT")
                        .and_then(|value| value.as_str())
                        .unwrap_or("?");
                    let message = entry
                        .get("MESSAGE")
                        .and_then(|value| value.as_str())
                        .unwrap_or("<no message>");
                    Line::from(format!("{unit}: {message}"))
                })
                .collect::<Vec<_>>()
        })
        .unwrap_or_else(|| vec![Line::from("loading…")]);
    frame.render_widget(
        Paragraph::new(lines)
            .block(Block::default().title(" Logs ").borders(Borders::ALL))
            .scroll((app.log_scroll, 0))
            .wrap(Wrap { trim: false }),
        area,
    );
}

fn render_settings(frame: &mut Frame<'_>, app: &mut App, area: Rect) {
    let rows = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(3),
            Constraint::Length(3),
            Constraint::Length(3),
            Constraint::Length(3),
            Constraint::Min(0),
        ])
        .split(area);

    let manage = if app.local_settings.manage_vpn_endpoints { "[x]" } else { "[ ]" };
    frame.render_widget(
        button(&format!("{manage} Manage VPN endpoints")),
        rows[0],
    );
    app.register_hitbox(rows[0], HitTarget::ToggleEndpointManagement);

    let active_profile = app
        .profiles
        .as_ref()
        .and_then(|profiles| profiles.active.as_deref())
        .unwrap_or("unmanaged");
    frame.render_widget(
        button(&format!("Default profile: [{active_profile} ▼]")),
        rows[1],
    );
    app.register_hitbox(rows[1], HitTarget::CycleProfile);

    let dns_areas = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Ratio(1, 2); 2])
        .split(rows[2]);
    let leshy_active = app.dns.as_ref().map(|dns| dns.provider == "leshy").unwrap_or(false);
    frame.render_widget(button(if leshy_active { "DNS: (*) Leshy" } else { "DNS: ( ) Leshy" }), dns_areas[0]);
    frame.render_widget(button(if leshy_active { "( ) System" } else { "(*) System" }), dns_areas[1]);
    app.register_hitbox(dns_areas[0], HitTarget::Dns(true));
    app.register_hitbox(dns_areas[1], HitTarget::Dns(false));

    let startup = app
        .status
        .as_ref()
        .map(|status| status.startup.enabled)
        .unwrap_or(false);
    frame.render_widget(
        button(if startup { "[x] Start service" } else { "[ ] Start service" }),
        rows[3],
    );
    app.register_hitbox(rows[3], HitTarget::ToggleStartup);

    frame.render_widget(
        Paragraph::new("Endpoint management is a local TUI safety switch. Provider-specific behavior stays behind the generic backend API.")
            .block(Block::default().title(" Notes ").borders(Borders::ALL))
            .wrap(Wrap { trim: true }),
        rows[4],
    );
}

fn render_footer(frame: &mut Frame<'_>, app: &App, area: Rect) {
    let help = "1-6 tabs  ↑↓ select  Enter apply  F5 refresh  q quit  mouse enabled";
    frame.render_widget(
        Paragraph::new(vec![Line::from(app.message.clone()), Line::from(help)])
            .alignment(Alignment::Left),
        area,
    );
}

fn button(label: &str) -> Paragraph<'_> {
    Paragraph::new(label)
        .alignment(Alignment::Center)
        .block(Block::default().borders(Borders::ALL))
}
