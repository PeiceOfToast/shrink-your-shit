// Settings' four tabs (U-C) and where each old Settings address now lives. Pure rules, so the test runs without a DOM.
// Every old address replaces itself with its new one (no Back step to it); a section keeps its #anchor.
const moved: Record<string, string> = {
  "/settings": "/settings/general",
  "/settings/general": "/settings/general",
  "/settings/appearance": "/settings/general#appearance",
  "/settings/processing": "/settings/processing",
  "/settings/safety": "/settings/processing#safety",
  "/settings/connections": "/settings/connections",
  "/settings/integrations": "/settings/connections",
  "/settings/notifications": "/settings/connections#notifications",
  "/settings/system": "/settings/system",
  "/settings/health": "/settings/system#health",
  "/settings/backup": "/settings/system#backup",
  "/settings/about": "/settings/system#about",
  "/settings/shares": "/libraries/shares",
  "/settings/variables": "/workflows?tab=variables",
};

// The address a path under Settings should open, or null when the path is not a Settings address.
export const settingsTarget = (path: string): string | null => moved[path] ?? null;

// The path part of an address (no ?query or #hash).
export const pathOf = (to: string): string => to.split(/[?#]/)[0];
