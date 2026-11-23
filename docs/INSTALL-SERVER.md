# Installing the Foghorn server

This covers installing, configuring, securing, backing up, upgrading, moving
and removing the server. If you only want it running, do **Part 1** and stop.

- [Part 1 — Install on Windows](#part-1-install-on-windows)
- [Part 2 — First sign-in](#part-2-first-sign-in)
- [Part 3 — Settings you might change](#part-3-settings-you-might-change) (port, HTTPS)
- [Part 4 — Looking after it](#part-4-looking-after-it) (backup, upgrade, move, remove, lost password)
- [Appendix A — Installing on Linux](#appendix-a-installing-on-linux)
- [Appendix B — Installing by hand, without the script](#appendix-b-installing-by-hand-without-the-script)
- [Appendix C — Running in Docker](#appendix-c-running-in-docker)

---

## What you need

| | |
|---|---|
| A machine that stays on | Windows Server 2016 or later, or Windows 10/11. Or a 64-bit Linux server with systemd — see [Appendix A](#appendix-a-installing-on-linux). Or any machine running Docker — see [Appendix C](#appendix-c-running-in-docker). A VM is fine. It does **not** need to be a domain controller and should not be one. |
| Resources | Tiny. About 40 MB of memory with 1,500 PCs connected. |
| Network | Every PC that should receive alerts must be able to reach this machine on one TCP port (8080 unless you change it). |
| A name for it | PCs will be told the server's address. A DNS name such as `foghorn01` or an alias like `foghorn.college.local` is better than an IP address, because you can move the server later without touching the PCs. |
| Rights | A local administrator on that machine. No domain rights are needed for the server. |

---

## Part 1 — Install on Windows

1. Copy the whole Foghorn folder to the server (anywhere — `C:\Install\Foghorn` is fine; it is only needed during install).

2. Click **Start**, type `powershell`, right-click **Windows PowerShell** and choose **Run as administrator**.

3. Go to the `deploy` folder and run the installer:

   ```powershell
   cd C:\Install\Foghorn\deploy
   powershell -ExecutionPolicy Bypass -File .\Install-FoghornServer.ps1
   ```

   To use a port other than 8080, add `-Port 9000` (or whichever).

4. After a few seconds you will see something like this. **Copy it somewhere safe for the next ten minutes** — you need the password once and the client key when you deploy the clients.

   ```
   Foghorn is running.

     Web console:  http://FOGHORN01:8080/

     Foghorn first-run details
     =========================
     Web console username : admin
     Temporary password   : hT4k-Wm2p-Qz8r-Lc5n
     Client key           : 3f9a1c…(48 characters)
   ```

That is the install finished. What the script did, so nothing is a mystery:

| It did this | Why |
|---|---|
| Copied `foghorn-server.exe` to `C:\Program Files\Foghorn Server` | Standard location, not writable by ordinary users. |
| Created `C:\ProgramData\Foghorn` and restricted it to Administrators and SYSTEM | It holds password hashes and the client key. |
| Registered the **Foghorn Alert Server** service (automatic start, restarts itself if it ever crashes) | So it survives reboots without anyone logging on. |
| Added an inbound Windows Firewall rule for the program (Domain and Private profiles) | So PCs and browsers can reach it. |

> **If it says the service is not answering:** something else is probably using
> the port. See [TROUBLESHOOTING.md](TROUBLESHOOTING.md#the-server-will-not-start).

---

## Part 2 — First sign-in

1. On any computer, open the **Web console** address the installer printed.
2. Sign in as `admin` with the temporary password.
3. You are asked to choose your own password straight away (at least 10 characters). Once you do, the file holding the temporary password deletes itself.
4. Open **Settings** and fill in **Organisation name**. It is shown at the top of every alert so people can see it is genuine.
5. Still in **Settings**, add an account for each person who will send alerts (**Add a person**). Give people their own accounts — every alert is signed with the sender's name.
   - **Administrator**: can do everything.
   - **Sender**: can send alerts only. You can limit a sender to particular groups, for example a tutor to their own classroom.

Next: [put the client on your PCs](DEPLOY-CLIENTS.md).

---

## Part 3 — Settings you might change

Server-level settings live in one small file:

```
C:\ProgramData\Foghorn\config.json
```

```json
{
  "listen": ":8080",
  "tls_cert": "",
  "tls_key": "",
  "trust_proxy_headers": false
}
```

Edit it in Notepad **run as administrator**, save, then restart the service:

```powershell
Restart-Service FoghornServer
```

| Setting | Meaning |
|---|---|
| `listen` | The port, written `":8080"`. To listen on one address only: `"10.0.0.5:8080"`. |
| `tls_cert`, `tls_key` | Paths to a certificate and its private key, in PEM format. Set both to turn on HTTPS. See below. |
| `trust_proxy_headers` | Leave `false` unless Foghorn sits behind your own reverse proxy (IIS/nginx) that sets `X-Forwarded-For`. If `true` when there is no proxy, anyone can fake their IP address in the logs. |

### Changing the port

Change `listen`, restart the service. The firewall rule follows the program,
not the port, so it needs no change. **PCs must be told the new address** —
update the Group Policy setting or re-run the client installer.

### Turning on HTTPS

Out of the box Foghorn uses plain HTTP, which means alert text and staff
passwords cross your network unencrypted. On a switched, internal network many
sites accept that for a pilot; for permanent use turn HTTPS on.
[SECURITY.md](SECURITY.md#https) has the reasoning. The steps:

1. **Get a certificate** for the name PCs will use (e.g. `foghorn.college.local`).
   - *If you have Active Directory Certificate Services:* request a **Web Server** certificate for that name. Domain PCs already trust your CA, so nothing needs deploying to them.
   - *If you don't:* any certificate works, but each PC must trust whoever issued it. Deploy the issuing CA certificate with Group Policy: *Computer Configuration → Policies → Windows Settings → Security Settings → Public Key Policies → Trusted Root Certification Authorities*.
2. **Export it as two PEM files** — the certificate (`foghorn.crt`, including any intermediate certificates after it) and the private key (`foghorn.key`, *without* a password). If you have a `.pfx`, OpenSSL converts it:

   ```
   openssl pkcs12 -in foghorn.pfx -clcerts -nokeys -out foghorn.crt
   openssl pkcs12 -in foghorn.pfx -nocerts -nodes  -out foghorn.key
   ```
3. Put both files in `C:\ProgramData\Foghorn\` (already restricted to administrators).
4. Edit `config.json`. Note the doubled backslashes — JSON requires them:

   ```json
   {
     "listen": ":8443",
     "tls_cert": "C:\\ProgramData\\Foghorn\\foghorn.crt",
     "tls_key": "C:\\ProgramData\\Foghorn\\foghorn.key",
     "trust_proxy_headers": false
   }
   ```
5. `Restart-Service FoghornServer`, then browse to `https://foghorn.college.local:8443/`.
6. Change the clients' server address to the new `https://…` one.

If the service will not start after this, the reason is on the last line of `C:\ProgramData\Foghorn\foghorn.log` (usually a path typo or a password-protected key).

---

## Part 4 — Looking after it

### Where everything is

| Path | Contents |
|---|---|
| `C:\Program Files\Foghorn Server\foghorn-server.exe` | The program. |
| `C:\ProgramData\Foghorn\config.json` | Port and HTTPS settings. |
| `C:\ProgramData\Foghorn\state.json` | Accounts, groups, templates, organisation name, client key. |
| `C:\ProgramData\Foghorn\alerts.json` | Alert history (the most recent 300 alerts). |
| `C:\ProgramData\Foghorn\clients.json` | Computers that have checked in (forgotten after 45 days' absence). |
| `C:\ProgramData\Foghorn\foghorn.log` | Who signed in, who sent what, errors. Rolls over at 10 MB. |

### Backing up

Copy the `C:\ProgramData\Foghorn` folder. That is everything. It is safe to
copy while the service is running. To restore: stop the service, put the folder
back, start the service.

### Upgrading

Run `Install-FoghornServer.ps1` from the new version's `deploy` folder, exactly
as in Part 1. It notices the existing service, stops it, swaps the program and
starts it again. Data and settings are untouched. Clients reconnect by
themselves within a minute.

### Moving to another machine

1. Install on the new machine (Part 1).
2. `Stop-Service FoghornServer` on **both** machines.
3. Copy `C:\ProgramData\Foghorn` from old to new, replacing what is there.
4. `Start-Service FoghornServer` on the new machine.
5. Point the DNS name at the new machine — or, if you used the machine's own name, change the clients' server address.
6. Uninstall from the old machine.

### Someone forgot their password

Another administrator: **Settings → Edit → Reset password**, and pass on the temporary password shown.

**The only administrator** is locked out: on the server, in an administrator PowerShell —

```powershell
cd "C:\Program Files\Foghorn Server"
.\foghorn-server.exe service stop
.\foghorn-server.exe reset-password admin
.\foghorn-server.exe service start
```

The second command prints a new temporary password. (It refuses to run while the service is up, because the running service would overwrite the change.)

### Service commands

```powershell
Get-Service FoghornServer          # is it running?
Restart-Service FoghornServer
cd "C:\Program Files\Foghorn Server"
.\foghorn-server.exe service status | start | stop
.\foghorn-server.exe version
```

### Removing it

```powershell
powershell -ExecutionPolicy Bypass -File .\Uninstall-FoghornServer.ps1              # keeps your data
powershell -ExecutionPolicy Bypass -File .\Uninstall-FoghornServer.ps1 -RemoveData  # removes everything
```

---

## Appendix A — Installing on Linux

The server runs just as well on Linux. **Only the server** — the desktop
client is Windows-only, and the PCs neither know nor care which operating
system the server is on. Everything in Parts 2–4 applies; this appendix gives
the Linux equivalent of each step.

### What you need

| | |
|---|---|
| Distribution | Any 64-bit (x86-64) Linux with systemd: Ubuntu 22.04+, Debian 12+, RHEL/Rocky/Alma 9+, and so on. A small VM or LXC container is plenty. |
| Architecture | `dist/foghorn-server-linux-amd64` is x86-64 only. For ARM (a Raspberry Pi, say) build it yourself: `cd server && GOOS=linux GOARCH=arm64 go build -mod=vendor -trimpath -ldflags "-s -w" -o ../dist/foghorn-server-linux-arm64 .` |
| Dependencies | None. It is a single static binary — no Go, .NET, database or web server needed. |
| Rights | `sudo`. The server itself runs as an unprivileged `foghorn` account. |

### A.1 Install

From the Foghorn folder (copied or `git clone`d onto the server):

```bash
# 1. A system account for the service to run as (no login, no home directory)
sudo useradd --system --home-dir /var/lib/foghorn --shell /usr/sbin/nologin foghorn

# 2. The program (inside the data folder, so it can update itself) and the systemd unit
sudo install -d -o foghorn -g foghorn -m 0750 /var/lib/foghorn /var/lib/foghorn/bin
sudo install -o foghorn -g foghorn -m 0755 dist/foghorn-server-linux-amd64 /var/lib/foghorn/bin/foghorn-server
sudo ln -sf /var/lib/foghorn/bin/foghorn-server /usr/local/bin/foghorn-server
sudo install -m 0644 deploy/linux/foghorn.service /etc/systemd/system/foghorn.service
#    RHEL family (SELinux) only - let systemd run a program from there:
#    sudo semanage fcontext -a -t bin_t '/var/lib/foghorn/bin(/.*)?' && sudo restorecon -Rv /var/lib/foghorn/bin

# 3. Start it now and at every boot
sudo systemctl daemon-reload
sudo systemctl enable --now foghorn

# 4. Check it is running, then read the first-run details
systemctl status foghorn --no-pager
sudo cat /var/lib/foghorn/FIRST-RUN.txt
```

`FIRST-RUN.txt` holds the temporary `admin` password and the client key —
exactly what the Windows installer prints. Continue with
[Part 2 — First sign-in](#part-2-first-sign-in) at `http://SERVER-NAME:8080/`.

What that did, so nothing is a mystery:

| It did this | Why |
|---|---|
| Created the `foghorn` system account | The service never runs as root. |
| Put the program in `/var/lib/foghorn/bin/`, with a link at `/usr/local/bin/foghorn-server` | So **Settings → Updates → Update server** can replace it ([UPDATING.md](UPDATING.md)). The link keeps `foghorn-server` working at the command line. |
| Made `/var/lib/foghorn` (mode `0750`, owned by `foghorn`) | It holds password hashes and the client key. |
| Started the service with a hardened sandbox | The unit can write **only** to `/var/lib/foghorn`; the rest of the system is read-only to it and `/home` is invisible. It restarts itself if it ever crashes. |

> **The service won't start and `systemctl status` shows `status=203/EXEC`?**
> The binary is not executable or, on RHEL-family systems, SELinux will not
> let systemd run it from `/var/lib`. Fix with
> `sudo chmod 0755 /var/lib/foghorn/bin/foghorn-server`, then the `semanage`
> and `restorecon` lines from step 2.

### A.2 Open the firewall

PCs and browsers must reach the server on its port (8080 by default).

```bash
sudo ufw allow 8080/tcp                                        # Ubuntu / Debian with ufw
sudo firewall-cmd --permanent --add-port=8080/tcp && sudo firewall-cmd --reload   # RHEL family
```

If you can, limit it to your client subnets rather than opening it to
everything, e.g. `sudo ufw allow from 10.20.0.0/16 to any port 8080 proto tcp`.

### A.3 Where everything is

| Path | Contents |
|---|---|
| `/var/lib/foghorn/bin/foghorn-server` | The program (`/usr/local/bin/foghorn-server` links to it). After an update, the one before is kept beside it as `foghorn-server.old`. |
| `/etc/systemd/system/foghorn.service` | The service definition. |
| `/var/lib/foghorn/config.json` | Port and HTTPS settings — same format as [Part 3](#part-3-settings-you-might-change). |
| `/var/lib/foghorn/state.json`, `alerts.json`, `clients.json` | Accounts, alerts, computers — as in [Part 4](#where-everything-is). |
| `/var/lib/foghorn/foghorn.log` | The application log. Also in the journal: `journalctl -u foghorn`. |

> Always pass `-data /var/lib/foghorn` when running `foghorn-server` by hand on
> Linux. Without it the program uses a `foghorn-data` folder in the current
> directory, not the service's data.

### A.4 Changing settings

```bash
sudo -u foghorn nano /var/lib/foghorn/config.json     # or any editor
sudo systemctl restart foghorn
```

The settings are the same as on Windows ([Part 3](#part-3-settings-you-might-change)).
Paths use forward slashes and need no doubling. If the service will not start
afterwards, the reason is in `journalctl -u foghorn -n 20`.

**Ports below 1024 (e.g. 443).** The service runs unprivileged, so it cannot
bind a low port by default. Either keep 8443 (simplest), or allow it:

```bash
sudo systemctl edit foghorn
#   add these two lines, save:
#   [Service]
#   AmbientCapabilities=CAP_NET_BIND_SERVICE
sudo systemctl restart foghorn
```

### A.5 HTTPS

Same reasoning and certificate advice as [Turning on HTTPS](#turning-on-https).
Then:

```bash
sudo install -o foghorn -g foghorn -m 0644 foghorn.crt /var/lib/foghorn/foghorn.crt
sudo install -o foghorn -g foghorn -m 0600 foghorn.key /var/lib/foghorn/foghorn.key
```

```json
{
  "listen": ":8443",
  "tls_cert": "/var/lib/foghorn/foghorn.crt",
  "tls_key": "/var/lib/foghorn/foghorn.key",
  "trust_proxy_headers": false
}
```

Restart, open the new port in the firewall, and change the clients' server
address to the `https://…` one. The certificate files must live somewhere the
service can read — `/var/lib/foghorn` is the easy choice; `/home` is hidden
from it by the sandbox.

**Behind nginx or another reverse proxy instead?** Have Foghorn listen on
`127.0.0.1:8080`, set `"trust_proxy_headers": true`, and make the proxy pass
`X-Forwarded-For` and `X-Forwarded-Proto` (subnet targeting and the logs rely
on the real client address). PCs hold each request open for about 25 seconds,
so the proxy's read timeout must be longer than that — nginx's default of 60
seconds is fine; do not lower it.

### A.6 Looking after it

| Task | Command |
|---|---|
| Is it running? | `systemctl status foghorn` |
| Restart / stop / start | `sudo systemctl restart foghorn` (or `stop`, `start`) |
| Recent log | `journalctl -u foghorn -n 50` or `sudo tail -f /var/lib/foghorn/foghorn.log` |
| Version | `foghorn-server version` |
| Back up | `sudo tar czf foghorn-backup-$(date +%F).tgz -C /var/lib foghorn` — safe while running |

**Upgrading** — from 1.2 on, use **Settings → Updates → Update server** in the
console ([UPDATING.md](UPDATING.md)). To do it by hand, or to move an older
install (program in `/usr/local/bin`) to the layout that can update itself:

```bash
sudo install -d -o foghorn -g foghorn -m 0750 /var/lib/foghorn/bin
sudo install -o foghorn -g foghorn -m 0755 dist/foghorn-server-linux-amd64 /var/lib/foghorn/bin/foghorn-server
sudo ln -sf /var/lib/foghorn/bin/foghorn-server /usr/local/bin/foghorn-server
sudo install -m 0644 deploy/linux/foghorn.service /etc/systemd/system/foghorn.service
sudo systemctl daemon-reload && sudo systemctl restart foghorn
```

Data and settings are untouched.

**Someone is locked out** (the only administrator):

```bash
sudo systemctl stop foghorn
sudo -u foghorn foghorn-server reset-password -data /var/lib/foghorn admin
sudo systemctl start foghorn
```

**Moving between Windows and Linux** (either direction) works: the data files
are the same. Stop both services, copy the *contents* of the data folder
across (`C:\ProgramData\Foghorn` ⇄ `/var/lib/foghorn`), on Linux run
`sudo chown -R foghorn:foghorn /var/lib/foghorn`, fix the `tls_cert`/`tls_key`
paths in `config.json` if you use HTTPS, then start the new one and move the
DNS name — as in [Moving to another machine](#moving-to-another-machine).

**Removing it:**

```bash
sudo systemctl disable --now foghorn
sudo rm /etc/systemd/system/foghorn.service /usr/local/bin/foghorn-server   # the program itself is in /var/lib/foghorn/bin
sudo systemctl daemon-reload
sudo rm -rf /var/lib/foghorn          # only if you also want to delete the data
sudo userdel foghorn
```

## Appendix B — Installing by hand, without the script

From an administrator prompt:

```bat
mkdir "C:\Program Files\Foghorn Server"
copy dist\foghorn-server.exe "C:\Program Files\Foghorn Server\"
mkdir C:\ProgramData\Foghorn
icacls C:\ProgramData\Foghorn /inheritance:r /grant:r *S-1-5-32-544:(OI)(CI)F *S-1-5-18:(OI)(CI)F
"C:\Program Files\Foghorn Server\foghorn-server.exe" service install
netsh advfirewall firewall add rule name="Foghorn Alert Server" dir=in action=allow program="C:\Program Files\Foghorn Server\foghorn-server.exe" profile=domain,private
type C:\ProgramData\Foghorn\FIRST-RUN.txt
```

To try it without installing anything, just run `foghorn-server.exe` in a
console window; it prints the first-run details and stops with Ctrl+C. (Run
that way it still keeps its data in `C:\ProgramData\Foghorn` unless you add
`-data C:\some\folder`.)

## Appendix C — Running in Docker

The repository has a `Dockerfile` (in `server/`) and a `docker-compose.yml`
(at the top). The image is about 25 MB, runs as an unprivileged user, and keeps
everything in one volume mounted at `/data`.

### C.1 Start it

On any machine with Docker and Docker Compose, from the repository folder:

```bash
docker compose up -d --build
docker compose logs foghorn          # the temporary admin password and client key
```

Browse to `http://<docker host>:8080/` and carry on from
[Part 2 — First sign-in](#part-2-first-sign-in). Open TCP 8080 on the host's
firewall so PCs can reach it, then point the clients at
`http://<docker host>:8080/`.

The first-run details are also in the volume, until you change the password:
`docker compose exec foghorn cat /data/FIRST-RUN.txt`.

### C.2 Settings

| What | How |
|---|---|
| Port on the host | Set `FOGHORN_PORT` in a `.env` file next to `docker-compose.yml` (e.g. `FOGHORN_PORT=8081`), then `docker compose up -d`. Leave `listen` in `config.json` at `:8080`: that is the port *inside* the container. |
| Time zone | `TZ` in `docker-compose.yml` (default `Europe/London`). **Schedules run on this clock**, so set it to yours. Containers otherwise run on UTC. |
| `config.json` | It is at `/data/config.json`. Edit it with `docker compose exec foghorn vi /data/config.json`, then `docker compose restart foghorn`. |

**HTTPS.** The simplest route is a reverse proxy (Caddy, Traefik, nginx) in
front of the container that handles the certificate. Set
`"trust_proxy_headers": true` in `config.json` so Foghorn sees the PCs' real
addresses. Alternatively, mount the certificate into the container (for
example add `- ./certs:/certs:ro` under `volumes:`), and set
`"tls_cert": "/certs/foghorn.crt"` and `"tls_key": "/certs/foghorn.key"` in
`config.json`. The files must be readable by user ID 10001.

**PCs' IP addresses.** Foghorn records each PC's address, and targeting rules
can use it (e.g. `10.20.0.0/16`). On a Linux Docker host with ports published
as above, Foghorn sees the real addresses. **Docker Desktop (Windows and Mac)
does not:** every PC appears to come from Docker's own address, so IP rules
will not work there. For production use a Linux host, or put a reverse proxy
in front and turn on `trust_proxy_headers`. If addresses still look wrong on
Linux, use `network_mode: host` in place of `ports:`.

### C.3 Looking after it

| Task | Command |
|---|---|
| Is it running? | `docker compose ps` (shows `healthy` when it answers on `/healthz`) |
| Log | `docker compose logs -f foghorn` |
| Restart / stop / start | `docker compose restart foghorn` (or `stop`, `start`) |
| Back up | `docker run --rm -v foghorn_foghorn-data:/data -v "$PWD":/backup alpine tar czf /backup/foghorn-backup.tgz -C /data .` (safe while running) |

The volume is named after the project folder, so `foghorn_foghorn-data` if
the folder is called `Foghorn`. `docker volume ls` shows the exact name.

**Upgrading.** From 1.3, **Update server** on the **Updates** page works in a
container too. A program replaced inside the image would be lost when the
container is recreated, so Foghorn saves the new version in the data volume
(`/data/bin/foghorn-server`) and exits. Docker's restart policy
(`restart: unless-stopped` in `docker-compose.yml`) starts it again. On every
start, the image's program hands over to the copy in the volume if that copy
is newer.

You can still update by rebuilding the image. The data volume is kept, and
an image newer than the copy in the volume takes over again:

```bash
git pull
docker compose up -d --build
```

If you start the container with `docker run` rather than Compose, give it
`--restart unless-stopped` so it comes back after an update. To undo an update
made from the console, delete `/data/bin` and restart the container. It then
runs the image's version again.

Rolling out new **client** versions from **Updates** works the same as on any
other server.

**Someone is locked out.** The server has to be stopped for this:

```bash
docker compose stop foghorn
docker compose run --rm foghorn reset-password admin
docker compose start foghorn
```

**Moving in or out of Docker.** The data files are the same as on Windows and
Linux. To move an existing install in, start the container once, stop it,
then copy the *contents* of the old data folder into the volume:

```bash
docker compose stop foghorn
docker run --rm -v foghorn_foghorn-data:/data -v /path/to/old-data:/old alpine \
  sh -c 'cp -a /old/. /data/ && chown -R 10001:10001 /data'
docker compose start foghorn
```

Fix any `tls_cert`/`tls_key` paths in `config.json`, and set `listen` back to
`:8080`.

**Removing it:** `docker compose down`, or `docker compose down -v` to delete
the data as well.
