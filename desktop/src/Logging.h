#pragma once

#include <QString>

// Debug logging for the desktop UI.
//
// Enabled by KIKIMORA_UI_DEBUG_LOG=<file>: every core command the UI sends, every reply it
// gets and every connection change lands in that file with a timestamp, so a stuck button can
// be traced from the click to the core and back.
void uiLog(const QString &message);
bool uiLogEnabled();
