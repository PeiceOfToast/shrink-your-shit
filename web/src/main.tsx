import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./styles/tokens.css";
import "./styles/shell.css";
import "./app.css";
import "./prefs"; // applies the saved appearance before the first paint of the app
import { App } from "./App";
import { call, failed } from "./api";
import { BASE } from "./router";
import { fromLink } from "./screens/SignIn";

// The sign-in link the server prints (?token=) signs in on any /next page, /next/signin included. The token leaves the
// address bar first (replaceState, so it isn't kept in history), then POST /api/login; the page goes on to where the
// link pointed (signin's next, or the Dashboard). A wrong token opens Sign in with the server's words, the field empty.
async function linkSignIn() {
  const u = new URL(location.href), token = u.searchParams.get("token");
  if (token === null) return;
  u.searchParams.delete("token"); history.replaceState(null, "", u.pathname + u.search + u.hash);
  const here = u.pathname.slice(BASE.length) || "/", want = here === "/signin" ? u.searchParams.get("next") ?? "/" : here + u.search + u.hash;
  const to = want.startsWith("/") && !want.startsWith("//") && !want.startsWith("/signin") ? want : "/";
  const r = await call("POST", "/api/login", { token: token.trim() });
  if (failed(r)) fromLink.error = r.error.message;
  history.replaceState(null, "", BASE + (failed(r) ? "/signin?next=" + encodeURIComponent(to) : to));
}

linkSignIn().catch(() => {}).finally(() => createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
));
