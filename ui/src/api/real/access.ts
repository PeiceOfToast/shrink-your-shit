// Real client, access group (contract section 1): setup, sign-in and out, languages, starters, guide, folders.
// Each export is annotated with the mock's type, so tsc enforces the same signature.
import type * as mock from "../mock.ts";
import { request, query, type Out } from "../http.ts";

export const getSetup: typeof mock.getSetup = () => request<Out<typeof mock.getSetup>>("GET", "/setup");
export const postSetup: typeof mock.postSetup = (input) => request<Out<typeof mock.postSetup>>("POST", "/setup", input);
export const signIn: typeof mock.signIn = (password) => request<Out<typeof mock.signIn>>("POST", "/signin", { password });
export const signInWithToken: typeof mock.signInWithToken = (token) =>
  request<Out<typeof mock.signInWithToken>>("POST", "/signin", { token });
export const signOut: typeof mock.signOut = () => request<Out<typeof mock.signOut>>("POST", "/signout");
export const getLanguages: typeof mock.getLanguages = () => request<Out<typeof mock.getLanguages>>("GET", "/languages");
export const getStarters: typeof mock.getStarters = () => request<Out<typeof mock.getStarters>>("GET", "/workflows/starters");
export const getStarter: typeof mock.getStarter = (id) =>
  request<Out<typeof mock.getStarter>>("GET", `/workflows/starters/${encodeURIComponent(id)}`);
export const getGuide: typeof mock.getGuide = () => request<Out<typeof mock.getGuide>>("GET", "/workflows/guide");
export const getFolders: typeof mock.getFolders = (path) =>
  request<Out<typeof mock.getFolders>>("GET", "/folders" + query({ path }));
export const getHealth: typeof mock.getHealth = () => request<Out<typeof mock.getHealth>>("GET", "/health");
export const checkFolder: typeof mock.checkFolder = (target) =>
  request<Out<typeof mock.checkFolder>>("POST", "/libraries/check", target);
// Settings > Access: the password (a session stays signed in; 204), and the access token (get, and rotate: the old one
// stops working at once). Session cookie only.
export const changePassword: typeof mock.changePassword = (password) =>
  request<void>("POST", "/access/password", { password });
export const getToken: typeof mock.getToken = () => request<Out<typeof mock.getToken>>("GET", "/access/token");
export const rotateToken: typeof mock.rotateToken = () => request<Out<typeof mock.rotateToken>>("POST", "/access/token/rotate");
