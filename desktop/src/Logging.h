#pragma once

#include <QString>

// Diagnostic logging for the desktop UI.
//
// Always on: every core command the UI sends, every reply it gets and every
// connection change lands in a daily file under the per-user data directory,
// so a stuck button can be traced from the click to the core and back. The
// core diagnostics capture copies those files on its own schedule.
// KIKIMORA_UI_DEBUG_LOG=<file> overrides the path (tests, portable runs).
void uiLog(const QString &message);
bool uiLogEnabled();
// Routes Qt warnings and criticals into the same file.
void installUiLogging();
