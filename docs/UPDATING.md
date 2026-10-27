# Updating Foghorn

From 1.2, the server and the PCs can be updated from the web console:
**Updates** in the menu (administrators only).

Updates come only from Foghorn's GitHub releases
(`github.com/archie-paice/Foghorn/releases`), downloaded over HTTPS and
checked against each release's published `SHA256SUMS.txt`. Nothing an
administrator types or uploads can become a program that runs on your PCs.
[How that is enforced](#why-updates-can-be-trusted) is at the end.

Foghorn still never contacts the internet by itself. The server checks
GitHub only when someone presses **Check for updates**, and PCs download a
release only after an administrator has started a rollout.

---

## Updating the server

1. **Updates → Check for updates.** The page shows the latest release and
   what has changed.
2. **Update server to X.** Foghorn downloads the server program for your
   operating system and checks it against the release's checksums. It runs
   the new program once to confirm it starts and reports the right version,
   then swaps it in and restarts. This takes about ten seconds. PCs reconnect
   by themselves, and the page reloads when the server is back.

The previous program is kept beside the new one as `foghorn-server.exe.old`
(Windows) or `foghorn-server.old` (Linux). To go back, stop the service, swap
the files and start it again.

| Server runs… | Can it update itself? |
|---|---|
| As the Windows service (`Install-FoghornServer.ps1`) | Yes. Windows restarts it on the new version. |
| Under systemd, installed as in [INSTALL-SERVER.md](INSTALL-SERVER.md#a1-install) from 1.2 | Yes. systemd restarts it on the new version. |
| Under systemd, installed before 1.2 (program in `/usr/local/bin`) | Not until you move it once: see [Upgrading](INSTALL-SERVER.md#a6-looking-after-it). The page tells you this. |
| In a command window (`foghorn-server run`) | It installs the update, then asks you to restart it yourself. |

## Updating the PCs

### Once: get 1.2 onto your PCs

The updater arrives with the 1.2 client. The 1.2 MSI installs a small Windows
service, **Foghorn Updater**, which is the same `FoghornClient.exe` running as
SYSTEM. PCs with an older client don't have it, so deploy the 1.2 MSI the way
you deployed Foghorn before ([DEPLOY-CLIENTS.md](DEPLOY-CLIENTS.md)). The
Updates page counts PCs that still have a client with no updater.

### Every release after that

1. **Updates → Check for updates.**
2. Under **PCs**, choose which computers, then **Roll out X**. Start with a
   test group of a few PCs: a group matching computer names such as `IT-*`,
   or one room.
3. Each chosen PC's updater checks with the server about every 20 minutes,
   and when the PC starts. It downloads the release's MSI from GitHub, checks
   it against the checksum the server gives it, and installs it. Foghorn on
   that PC closes for a few seconds, then opens again for everyone signed in.
   Locks and unanswered alerts come straight back.
4. Watch the progress bar and the list of PCs. When the test group looks
   right, choose **Everyone** and roll out again.

**Pause** stops more PCs starting the update; PCs part-way through finish.
**Stop** ends the rollout. PCs never go back to an older version.

Rollouts target whole computers, so choose them by saved group, computer
name, OU or IP address. Usernames and users' AD groups don't apply.

To make one PC check straight away, run this on it as an administrator:

```bat
"C:\Program Files\Foghorn\FoghornClient.exe" --update-now
```

### When a PC does not update

The PC list on the Updates page shows each updater's state and, for
failures, the reason. The updater's own log is on the PC, readable by
administrators:

```
C:\Windows\System32\config\systemprofile\AppData\Local\Foghorn\updater\updater.log
```

The `install-<version>.log` next to it is the full Windows Installer log.

| The PC says | Meaning and fix |
|---|---|
| *Could not download the update. GitHub: …* | The PC can't reach `github.com` and `*.githubusercontent.com` as SYSTEM, usually because of a proxy or web filter. Allow those for PCs, or turn on HTTPS on the Foghorn server so PCs can use its copy instead (below). |
| *…it is not on HTTPS, so it is not used* | The server has a copy of the MSI, but PCs only fetch it over HTTPS. Turn on [HTTPS](INSTALL-SERVER.md#turning-on-https), or let PCs reach GitHub. |
| *the file did not match the checksum* | The download was damaged or altered on the way. It is retried later. If it keeps happening, something between the PC and GitHub is changing files. |
| *msiexec failed with exit code …* | Windows Installer refused. See `install-<version>.log`. A failed update rolls back, so the PC keeps the version it had. A failed version is retried after six hours. |

**PCs without internet access.** If PCs can't reach GitHub, the server keeps
its own copy of the MSI being rolled out. PCs use it only if the server is on
HTTPS ([why](#why-updates-can-be-trusted)). With neither GitHub access nor
HTTPS, deploy new versions the usual way: the MSI through Group Policy,
Intune or SCCM.

### If you deploy with Group Policy, Intune or SCCM

Those tools keep track of the version *they* deployed. When the updater moves
a PC on to a newer version, they may treat their version as missing and try
to put it back. The MSI refuses to downgrade, so the PC keeps the newer
version, but the tool may report an error on every attempt. Two ways to
avoid that:

- **Use the tool once, then let Foghorn update.** Deploy the 1.2 MSI, then
  remove the Group Policy assignment and choose *Allow users to continue to
  use the software*. In Intune or SCCM, retire the deployment without
  uninstalling it. PCs keep Foghorn, and updates come from the console from
  then on.
- **Or keep updating through the tool** and don't use rollouts. Your tool
  stays in charge of versions. In Intune or SCCM, detect Foghorn by its
  UpgradeCode `{7B0E5C0A-6E0B-4B7E-9C55-5A1C1F0D7A11}` with a minimum version,
  not by ProductCode, so a newer version counts as installed.

New PCs still need the MSI from somewhere the first time, so a deployment for
new machines is still useful. Point it at the latest MSI.

---

## Why updates can be trusted

The updater runs as SYSTEM on every PC, so it is guarded closely:

- **The PC decides where the file comes from, not the server.** The server
  only says *which version* to install. Each PC builds the download address
  itself: `https://github.com/archie-paice/Foghorn/releases/download/v<version>/FoghornClient.msi`.
  The repository is built into the client, and only an administrator can
  change it, with the `UpdateRepo` value under
  `HKLM\SOFTWARE\Policies\Foghorn` (for forks). So a hacked Foghorn server, or
  someone tampering with its plain-HTTP traffic, can at most pick another
  genuine Foghorn release. It can never pick an older one, because PCs don't
  downgrade.
- **HTTPS all the way.** The download must stay on HTTPS, including GitHub's
  redirect to its storage host. The file must match the release checksum the
  server passed on, and that checksum came from GitHub over HTTPS too.
- **The server's copy is used only over HTTPS.** Over plain HTTP, anyone able
  to intercept traffic on your network could swap both the file and its
  checksum. With HTTPS, the PC also checks it is talking to the real server.
- **Nothing can be swapped in between.** Downloads go to SYSTEM's own profile
  folder, which students can't write to. The checked file is held open and
  locked against changes until Windows Installer has finished with it.
- **Failed updates roll back.** The old version is removed inside the same
  installation transaction as the new one goes in, so if anything fails, the
  PC is left on the version it had, not without a client.
- **The server checks its own update the same way:** HTTPS from GitHub, the
  release checksum, and a trial run, before anything is replaced.

What this does *not* protect against is someone taking over the GitHub
repository itself. Releases are only as trustworthy as the GitHub account
that publishes them, so protect it with two-factor authentication.

## Publishing a release (maintainers)

1. Bump the version in `server/main.go`, `client/src/Program.cs` (three
   places) and `deploy/msi/FoghornClient.wxs`, and add a `## X.Y.Z` section
   to `CHANGELOG.md`.
2. Rebuild everything in `dist/` ([BUILDING.md](BUILDING.md)):
   `client\build.cmd`, the two `go build`s, then
   `deploy\msi\Update-MsiClient.ps1`, which also refreshes `SHA256SUMS.txt`.
3. Merge to `main`, then tag it:
   ```bash
   git tag v1.2.0 && git push origin v1.2.0
   ```
   The **Release** workflow checks that the tag, server, client and MSI
   versions agree and that `SHA256SUMS.txt` matches `dist/`. It then
   publishes the release, using that version's CHANGELOG section as the notes.
   From then on, servers find it with **Check for updates**.
