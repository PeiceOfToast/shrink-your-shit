// Shares: the intro and the line under the setup code box, keyed on the server's mode AND why it is that mode.
// Pure, so a test can pin all four cases (the wording is design's; see briefs/shares-manual-unpriv-wording.md).

const UNPRIV_COMPOSE: [string, string] = [
  "This container can't connect network shares itself. Mount the share on the Proxmox host, pass the folder into the container, then add this line to your compose file and restart it.",
  "The Proxmox host keeps it connected. Press Check again once the container is back up.",
];
const UNPRIV_MANUAL: [string, string] = [
  "This container can't connect network shares itself. Run these steps on the Proxmox host, as root. They mount the share there and pass the folder into this container.",
  "The Proxmox host keeps it connected. Press Check again once the container is back up.",
];
const DOCKER: [string, string] = [
  "Add these lines to your compose file, then restart the container.",
  "Docker connects it each time the container starts. Press Check again once it's back up.",
];
const MANUAL: [string, string] = [
  "Run this once on this server, as root:",
  "It connects the share now and after every restart.",
];

export function shareIntro(mode: string | null | undefined, modeReason: string | null | undefined): [string, string] {
  const unpriv = modeReason === "unprivileged_container";
  if (mode === "docker") return unpriv ? UNPRIV_COMPOSE : DOCKER;
  return unpriv ? UNPRIV_MANUAL : MANUAL;
}
