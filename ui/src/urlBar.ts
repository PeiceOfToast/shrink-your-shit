// Puts a URL in the address bar without a move: the page stays as it is (no popstate, no re-render from the router), and a
// reload opens that URL. Plain module, so the node tests can load it without the router's JSX.
export function replaceUrl(to: string): void {
  if (to !== location.pathname + location.search) history.replaceState(null, "", to);
}
