# Security

A tool that can put any message, full-screen, on every PC in the building is
worth protecting. This page says plainly what Foghorn protects, what it does
not, and what to do about the gaps. Read it before going live.

## Checklist before going live

- [ ] Every sender has **their own account**. Nobody shares `admin`.
- [ ] Staff who only need one room are **Senders limited to that group**.
- [ ] The server machine is one only IT staff can log on to.
- [ ] You have decided about **HTTPS** (see below) — and if staying on HTTP, you know what that exposes.
- [ ] The Foghorn port is reachable **only from inside your network**. Never forward it from the internet.
- [ ] In Docker: Docker's published ports can bypass host firewalls such as `ufw`. Check the port is not reachable from outside, or publish it on one internal address only (e.g. `"10.0.0.5:8080:8080"` in `docker-compose.yml`).
- [ ] You have agreed who may send **Urgent / Full screen** alerts and when. That is a policy question, not a technical one, and it matters most.

## Who can do what

| | Needs | Can |
|---|---|---|
| **Administrator** | Username + password | Everything: send, manage accounts, groups, settings, see the client key. |
| **Sender** | Username + password | Send alerts (optionally only to chosen groups), recall their own alerts, see computers and history. Cannot see settings or the client key. |
| **Sender allowed to lock** | As a sender, plus *Can lock screens* ticked by an administrator | Also lock and unlock screens, only on computers they may send to. |
| **API token** | The token | Only its scopes: `send` (send, recall, schedule), `lock` (lock and unlock screens), `read` (read history and the computer list). Never settings, accounts, groups, webhooks or uploads. |
| **A PC with the client key** | The client key | **Receive** alerts and report that it showed them. It cannot send anything. |
| **Administrator, for updates** | As administrator | Update the server, and roll out client versions to PCs, but **only genuine releases from Foghorn's GitHub**. No one can upload or point PCs at any other file ([Updates](#updates)). |

## What is built in

- **Passwords** are stored as salted PBKDF2-SHA256 hashes (210,000 rounds), never in readable form. Minimum 10 characters. New and reset accounts get a random temporary password that must be changed at first sign-in.
- **Lock-out:** five wrong passwords locks that username, and that network address, out for five minutes.
- **Sessions** last 12 hours, in a cookie that scripts cannot read and that browsers will not send from other sites. Every change also requires a header that other websites cannot add, which blocks cross-site request forgery.
- **Nothing a PC reports is trusted as HTML.** Computer names, usernames and the rest are always shown as plain text in the console, and alert text is always shown as plain text on desktops, so neither side can inject code into the other.
- **Links** in alerts must be `http://` or `https://`. The client checks again before opening one and hands nothing else to Windows.
- **The client runs as the logged-on user**, without administrator rights, and listens on no ports.
- **Audit trail:** `foghorn.log` on the server records every sign-in (and failure), every alert with its sender and audience, every recall, and account changes. Alert history in the console shows the sender's name.
- **The data folder** is restricted to Administrators and SYSTEM by the installer.
- **API tokens** are stored only as SHA-256 hashes and shown once.

## HTTPS

By default Foghorn speaks plain HTTP. On the wire that means:

| Can be read by someone able to capture traffic on your network | |
|---|---|
| Alert text | Yes |
| Staff passwords, at sign-in | **Yes** |
| The client key | Yes |

On a modern switched network, capturing other people's traffic takes deliberate
effort (ARP spoofing, a mirrored port), but a computing college is exactly
where someone might try. **Turn HTTPS on for permanent use.** It takes about
fifteen minutes with an internal CA — see
[INSTALL-SERVER.md → Turning on HTTPS](INSTALL-SERVER.md#turning-on-https).
With HTTPS on, all three rows above become "No", and clients also verify they
are talking to the real server. The client will refuse to connect to a server
whose certificate the PC does not trust; there is deliberately no "ignore
certificate errors" switch.

If you stay on HTTP for a pilot: sign in to the console only from wired,
staff-side machines, and use a password you use nowhere else.

## The client key

Every PC holds the client key, in a place any logged-on user can read (it has
to be — the client runs as that user). If you deploy with script parameters it
is also readable in SYSVOL by any domain user. Treat it as **known to your
students**, and consider what it gives them:

- They can run their own copy of the client, or write a script that pretends to be any computer or user, and so **receive** alerts meant for others, and clutter the **Computers** list with fake entries.
- They **cannot send, change or recall** alerts, and cannot sign in to the console.

So: do not put anything in an alert that would matter if a student elsewhere in
the building read it. For Foghorn's purpose — "save your work", "leave the
building" — that is rarely a constraint.

If the key leaks outside the organisation, or you just want a clean slate:
**Settings → Client key → Replace key…**, then update the key in your GPO. PCs
stop receiving alerts until they have the new key, so with the script-parameter
method that means until their next restart; with the
[ADMX method](DEPLOY-CLIENTS.md#appendix-a-setting-the-server-address-with-the-group-policy-template-admx)
it is the next policy refresh.

## Students closing the client

The client runs as the student, so the student can end it from Task Manager.
Foghorn does not pretend otherwise. What helps:

1. **The watchdog.** The script installer adds a scheduled task that starts the client again within five minutes. (The MSI does not.)
2. **The Computers page** shows a grey dot against any PC whose client is not connected, so a tutor can see who has dropped off.
3. **Remove Task Manager for students** if you have not already: *User Configuration → Administrative Templates → System → Ctrl+Alt+Del Options → Remove Task Manager*. Most school builds already do.
4. If your PCs use **AppLocker or WDAC**, `C:\Program Files\Foghorn\` is covered by the default "Program Files" allow rules, and students cannot write there, so they cannot swap the program for their own.

For a life-safety message, Foghorn should be **one channel among several** —
alongside the fire alarm, tannoy and people — never the only one.

## Screen locks

**Lock screens** covers the chosen PCs (or freezes them with the screen
visible) and blocks keyboard and mouse. It is a classroom-management tool,
not a security boundary. Here is exactly what it does:

- **Blocked:** every key, including Alt+Tab, the Windows key, Alt+F4,
  Ctrl+Esc and Ctrl+Shift+Esc. The mouse pointer is frozen. Every monitor is
  covered by an always-on-top window.
- **Not blocked: Ctrl+Alt+Del.** Windows reserves it, and no program running
  as the user can intercept it, by design. From there a student can **sign
  out**, and, unless you have removed it for students, open **Task Manager**
  and end the client. If they sign in again while the lock is still in force,
  the lock comes straight back. The lock's page shows which PCs are still
  locked, so a PC that has dropped out is visible. Removing Task Manager for
  students (see above) closes the second route.
- **Always ends.** Every lock has a time limit (at most 8 hours). The PC
  enforces that limit itself, so a lock lifts on time even if the PC can no
  longer reach the server. If something goes wrong, signing out always frees a
  PC.
- **Alerts still get through.** Alerts appear above a lock and can be clicked,
  so an evacuation alert sent to a locked room can still be read and answered.
- **Who can lock:** administrators, senders an administrator has allowed, and
  API tokens with the `lock` scope. Senders restricted to certain groups can
  lock only those. Every lock and unlock is in `foghorn.log` with who did it.

## Webhooks

Webhook addresses are set by administrators only. Each delivery is signed
(HMAC-SHA256 over the timestamp and body) with a per-webhook secret, so the
receiver can tell genuine deliveries from forged ones and refuse old ones that
are replayed later. Deliveries contain alert titles, computer names and
usernames, so send them only to systems that may hold that information, and
use `https://` addresses. Foghorn does not follow redirects when delivering.

A Discord webhook URL works as its own password: anyone who has it can post to
the channel. Foghorn shows only the start of it in Settings and keeps it out
of the server log, but treat it like a password. If it leaks, delete the
webhook in Discord and make a new one.

The `api.call` event, which is only sent to webhooks that tick it, gives an
audit trail of changes made through API tokens. It records the token's name,
the method and path, the answer's status and the caller's IP. It never
includes the token or the request body.

## Updates

The PC updater runs as SYSTEM, so it would be the most valuable thing in
Foghorn to subvert. It's built so that neither the console nor the network
can choose what it installs:

- **The server names a version, never a file.** Each PC builds the GitHub
  download address itself, from a repository built into the client. Only an
  administrator can change that, with a Group Policy value, for forks.
- **HTTPS from GitHub, with the release's checksum.** Redirects must stay on
  HTTPS. PCs never downgrade.
- **The server's own copy is used only over HTTPS**, because over plain HTTP
  the file and its checksum could both be swapped on the way.
- **Downloads go where students can't write** (SYSTEM's profile), and the
  checked file stays locked until Windows Installer has finished with it.
- **A failed update rolls back** to the version the PC had.

So a stolen administrator password, or traffic interception on your network,
can at most move PCs to another *genuine, newer* Foghorn release. The
remaining trust is in the GitHub account that publishes releases: protect it
with two-factor authentication. Full detail in
[UPDATING.md](UPDATING.md#why-updates-can-be-trusted).

## Things Foghorn deliberately does not do

- It does not prevent logging off. A lock blocks the keyboard and mouse, but Ctrl+Alt+Del → Sign out always works (see [Screen locks](#screen-locks)).
- It does not authenticate individual PCs. Doing that properly means a certificate per machine, which is a lot of machinery for the risk described above.
- It does not contact the internet by itself. The server checks GitHub only when an administrator presses **Check for updates**, or once a day if an administrator turns on **Check for new versions once a day** (off by default). That daily check is a single request to GitHub's releases API and sends nothing about your school. PCs download from GitHub only during a rollout an administrator started. Nothing goes outside your network otherwise, unless an administrator adds a webhook pointing there. No telemetry, no external fonts or scripts in the console.

## Reporting what the PCs send

So you can answer a data-protection question: each client sends the server its
computer name, the logged-on Windows username and domain, the computer's AD
distinguished name (its OU), the names of the AD groups in the user's log-on
token, its Windows version, and its client version. The server adds the IP
address and the time. When an alert has reply buttons, the reply each person
picks is stored with that computer's delivery record. Computers not seen for 45 days are forgotten
automatically; an administrator can **Forget** one sooner from the Computers
page. Alert history keeps per-computer detail for the most recent 60 alerts and
totals only for older ones, up to 300.
