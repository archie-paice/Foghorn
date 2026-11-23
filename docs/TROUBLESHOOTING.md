# Troubleshooting

Find your symptom, work down the list. The two most useful tools:

| Tool | Where | Tells you |
|---|---|---|
| `"C:\Program Files\Foghorn\FoghornClient.exe" --status` | On the PC, as the affected user | Which settings the client is using, where they came from, and whether it can reach the server — with the reason if not. |
| `C:\ProgramData\Foghorn\foghorn.log` | On the server | Start-up errors, sign-ins, every alert sent. |

Other logs: `%LOCALAPPDATA%\Foghorn\client.log` (per user, on the PC) and
`C:\Windows\Temp\FoghornClient-install.log` (the installer, on the PC).

- [A PC never appears in the console](#a-pc-never-appears-in-the-console)
- [The PC shows in the console but alerts do not arrive](#the-pc-shows-in-the-console-but-alerts-do-not-arrive)
- [Alerts look wrong](#alerts-look-wrong)
- [The server will not start](#the-server-will-not-start)
- [I cannot open or sign in to the web console](#i-cannot-open-or-sign-in-to-the-web-console)
- [Targeting by OU or AD group matches nothing](#targeting-by-ou-or-ad-group-matches-nothing)
- [The Group Policy script is not installing the client](#the-group-policy-script-is-not-installing-the-client)

---

## A PC never appears in the console

Someone must be **logged on** — the client runs in the user's session, so a PC
at the sign-in screen is invisible to Foghorn.

1. **Is the client installed?** Does `C:\Program Files\Foghorn\FoghornClient.exe` exist? If not → [the Group Policy script is not installing the client](#the-group-policy-script-is-not-installing-the-client).
2. **Is it running?** Task Manager → Details → `FoghornClient.exe`. If not, start it by double-clicking it; if it then works, the log-on start is the problem — check `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Run` has a `FoghornClient` value, and that no policy blocks Run-key programs.
3. **Run `--status`.** Read the last lines:

| `--status` says | Meaning and fix |
|---|---|
| `Server address: NOT SET` | The client has no settings. Check the Script Parameters in the GPO (or the ADMX policy) and that the PC has restarted / refreshed policy since. |
| `…rejected the client key` | The key on the PC differs from **Settings → Client key**. Usually a copy-paste slip (a missing character, a trailing space), or the key was replaced after the PCs were set up. |
| `The server name could not be found in DNS` | Typo in the address, or the PC cannot resolve that name. Try `ping foghorn01` on the PC. |
| `No answer from the server: …` | The PC cannot reach the port. On the PC run `Test-NetConnection foghorn01 -Port 8080` in PowerShell. If `TcpTestSucceeded` is `False`: is the service running? is the server's firewall rule present? is there a firewall or VLAN rule between the student network and the server? |
| `The server answered with HTTP 404` (or another number) | The address points at something that is not Foghorn — wrong port, or another web server. |
| `This PC does not trust the server's HTTPS certificate` | Deploy the issuing CA to the PCs' Trusted Root store, and make sure the address uses the exact name on the certificate. |
| `Connection test: OK` | The client is fine. Refresh the Computers page; clear any filter; untick "Only show computers that are on". |

## The PC shows in the console but alerts do not arrive

1. **Does the alert's audience include it?** Open the alert under **Sent alerts**. If the PC is not in the list, the targeting did not match it — check the group's rules against the PC's name/OU on the Computers page.
2. **Is the dot green?** Grey means the client is not connected right now.
3. **Was it "only computers on right now"?** Then a PC that connected a moment later will not get it.
4. **Is it listed as "Sent, waiting for the PC to confirm"?** The server handed it over but the client did not report showing it. Look at the end of `%LOCALAPPDATA%\Foghorn\client.log` on the PC for an error.
5. **Four alerts already on screen?** At most four cards show at once; the rest queue until one is closed.

## Alerts look wrong

Run `FoghornClient.exe --test` to reproduce without a server.

| Symptom | Likely cause |
|---|---|
| Appears **behind** another window | Another always-on-top program (some exam browsers, screen-sharing toolbars, full-screen games using exclusive mode). Foghorn re-asserts itself every 1.5 seconds, so it should come back on top; exclusive-mode full-screen apps can still hide it. |
| Text blurry | Windows is scaling the program instead of the program scaling itself. Right-click the exe → Properties → Compatibility → *Change high DPI settings* should all be **unticked**. |
| Card appears on the "wrong" monitor | Corner and centre alerts use the **primary** display. Full-screen alerts cover all displays. |
| Text cut off at the bottom of a card | Please report it with the Windows display-scaling percentage; as a workaround use a shorter message or the Centre style. |
| Full-screen alert is on top but typing still goes to the app underneath | Windows refused to give a background program the keyboard. The alert is still visible and the button works with the mouse. |
| No sound | The PC is muted / has no speakers, or Windows' "Asterisk / Exclamation / Critical Stop" sounds are set to *None* in the sound scheme. |

## The server will not start

Look at the **last few lines** of `C:\ProgramData\Foghorn\foghorn.log`.

| Log says | Fix |
|---|---|
| `cannot listen on :8080 … address already in use` (or "Only one usage of each socket address") | Another program has the port. Find it: `Get-NetTCPConnection -LocalPort 8080 -State Listen \| Select OwningProcess` then `Get-Process -Id <number>`. Either stop that program or change `listen` in `config.json` to a free port and tell the clients the new address. |
| `config.json is not valid JSON` | A typo in the file. The common ones: single backslashes in paths (must be `\\`), a missing comma or quote. |
| `cannot load HTTPS certificate` | Wrong path, the key file has a password, or the files are not PEM text (open them in Notepad: they should start `-----BEGIN`). |
| Nothing new in the log at all | The service is not launching the program. In `services.msc` check **Foghorn Alert Server** → *Path to executable* points at a file that exists. Re-run `Install-FoghornServer.ps1`. |

To watch it start in front of you: `Stop-Service FoghornServer`, then run
`"C:\Program Files\Foghorn Server\foghorn-server.exe"` in an administrator
console. Errors print there. Ctrl+C, then `Start-Service FoghornServer`.

**In Docker**, the same messages are in `docker compose logs foghorn`. If
`docker compose up` says `port is already allocated`, something else on the
host has that port. Set `FOGHORN_PORT` to a free one in a `.env` file next to
`docker-compose.yml`. If the log says it cannot write to `/data`, the volume is
a host folder that the container's user (ID 10001) cannot write to. Fix it with
`sudo chown -R 10001:10001 <that folder>`, or use the named volume from
`docker-compose.yml`. See
[INSTALL-SERVER.md, Appendix C](INSTALL-SERVER.md#appendix-c-running-in-docker).

## I cannot open or sign in to the web console

- **Page will not load from another PC but works on the server itself** (`http://localhost:8080/`): the firewall. Re-run the installer, or check the *Foghorn Alert Server* inbound rule exists and that the server's network is classed as Domain or Private, not Public.
- **"Too many failed sign-ins"**: wait five minutes. (Restarting the service also clears it.)
- **Forgotten the only admin password**: [INSTALL-SERVER.md → Someone forgot their password](INSTALL-SERVER.md#someone-forgot-their-password).
- **Signed out unexpectedly**: sessions last 12 hours and are forgotten when the service restarts.

## Targeting by OU or AD group matches nothing

Look at what the PC actually reports: **Computers** page → the **OU** column
(hover for the full distinguished name) and hover over the **user** for their
groups. `--status` on the PC shows the same.

- **OU blank**: the PC is not domain-joined, or Group Policy has never applied on it. The client reads the computer's distinguished name from where Group Policy caches it.
- **OU rule not matching**: an OU rule matches any part of the distinguished name, so `OU=Lab 1` works, and so does `Lab 1`; `Lab1` does not if the OU is called `Lab 1`. It is the **computer's** OU, not the user's.
- **Group rule not matching**: use the group's name as shown in the hover list, e.g. `Students-Year1` or `COLLEGE\Students-Year1`. Groups come from the user's log-on token, so a user added to a group must **log off and on** before it counts. Local and built-in groups are left out on purpose.

## The Group Policy script is not installing the client

On an affected PC:

1. `type C:\Windows\Temp\FoghornClient-install.log`
   - **File does not exist** → the script never ran. Go to 2.
   - **`FAILED: FoghornClient.exe is not next to this script`** → the exe is missing from the share folder.
   - **`FAILED: This must run as an administrator`** → it was added as a *user* log-on script. It must be under **Computer Configuration → … → Scripts → Startup**.
2. `gpresult /r /scope computer` — is the `Foghorn client` GPO under *Applied Group Policy Objects*? If not: is the GPO linked to the OU the **computer** is in? Is it security-filtered away from the computer?
3. Did the PC do a real **restart** since the GPO was linked? (*Shut down* with Fast Startup does not run Group Policy start-up processing — see [DEPLOY-CLIENTS.md, Step 5](DEPLOY-CLIENTS.md#step-5-check-it-worked).)
4. Can the computer account read the share? From an administrator prompt on the PC: `psexec -s cmd` (Sysinternals) then `dir \\YOURDOMAIN\NETLOGON\Foghorn`. Simpler: check the folder's permissions include *Authenticated Users* or *Domain Computers* with Read.
5. Is **Always wait for the network at computer startup and logon** enabled? Without it the script can run before the network is up. Event Viewer → *Applications and Services Logs → Microsoft → Windows → GroupPolicy → Operational* shows script processing and any failure.
