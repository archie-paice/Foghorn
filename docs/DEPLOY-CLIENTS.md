# Deploying the Foghorn client

The client is one small program, `FoghornClient.exe` (about 64 KB). It needs
**.NET Framework 4.x, which is part of Windows 10 and 11**, so there is nothing
else to install on the PCs. It runs silently as whoever is logged on, shows
nothing until an alert arrives, and never runs with administrator rights.

The recommended path is **MSI + transform, deployed with Group Policy Software
Installation**. It is standard Windows administration, cleanly reversible, and
keeps your client key out of the MSI itself. If you prefer a start-up script,
Appendix B has it.

Do the steps in order. Step 1 takes five minutes and saves hours.

- [Before you start](#before-you-start)
- [Step 1 — Pilot on one PC by hand](#step-1-pilot-on-one-pc-by-hand)
- [Step 2 — Make the transform (`.mst`)](#step-2-make-the-transform-mst)
- [Step 3 — Put the files on a share](#step-3-put-the-files-on-a-share)
- [Step 4 — Create the Group Policy Object](#step-4-create-the-group-policy-object)
- [Step 5 — Check it worked](#step-5-check-it-worked)
- [Upgrading, rotating the key, removing](#upgrading-rotating-the-key-removing)
- [Appendix A — Setting the server address with the Group Policy template (ADMX)](#appendix-a-setting-the-server-address-with-the-group-policy-template-admx)
- [Appendix B — Start-up script instead of MSI](#appendix-b-start-up-script-instead-of-msi)
- [Appendix C — Intune, PDQ Deploy, SCCM and similar](#appendix-c-intune-pdq-deploy-sccm-and-similar)
- [Appendix D — Reference: what the installer changes, settings, command line](#appendix-d-reference-what-the-installer-changes-settings-command-line)

## Before you start

You need the server running ([INSTALL-SERVER.md](INSTALL-SERVER.md)) and two
pieces of information:

| | Example | Where to find it |
|---|---|---|
| **Server address** | `http://foghorn01:8080` | Printed by the server installer. It is the console address without the trailing `/`. |
| **Client key** | `3f9a1c…` (48 characters) | Web console → **Settings** → **Client key** → **Copy**. |

## Step 1 — Pilot on one PC by hand

Pick one typical PC.

1. Copy `dist\FoghornClient.exe`, `deploy\Install-FoghornClient.cmd` and
   `deploy\Install-FoghornClient.ps1` into one folder on it, e.g.
   `C:\Temp\Foghorn`.

2. **See what alerts look like.** Open a normal Command Prompt in that folder:

   ```bat
   FoghornClient.exe --test
   ```

   Two sample alerts appear (corner and centre). Close both and a full-screen
   one appears. Check: are they on top of your other windows? Is the text
   sharp? Can you keep typing in another window while the corner alert is
   showing? If anything looks wrong, see
   [TROUBLESHOOTING.md](TROUBLESHOOTING.md#alerts-look-wrong).

3. **Install it.** Open Command Prompt **as administrator** in that folder:

   ```bat
   Install-FoghornClient.cmd -ServerUrl http://foghorn01:8080 -ClientKey PASTE-THE-KEY
   ```

   It should finish with `Foghorn client is installed.`

4. **Check the connection.** As a normal user:

   ```bat
   "C:\Program Files\Foghorn\FoghornClient.exe" --status
   ```

   A box shows the settings in use and ends with `Connection test: OK`. If it
   says `FAILED`, the box tells you why — fix that before going further.

5. **Send yourself an alert.** Log off, log on as a test user. In the web
   console the PC appears under **Computers** with a green dot within a few
   seconds. Choose **Send alert** on that row and send something.

6. **Check the targeting information.** On the **Computers** page, confirm the
   **OU** column is right for that PC, and hover over the username to see the
   AD groups the client reported. If you plan to target by OU or group, this
   is what the rules will match against.

When all six work, carry on.

## Step 2 — Make the transform (`.mst`)

A **transform** is a small file Windows Installer applies on top of an MSI at
install time. Your `FoghornClient.msi` is generic — it contains no server
address or key. The transform carries those, so you can publish the MSI on any
share and keep one transform per site (or per key rotation).

On any Windows PC (no elevation needed), open PowerShell in `deploy\msi\` and
run:

```powershell
.\New-FoghornTransform.ps1 `
    -ServerUrl http://foghorn01:8080 `
    -ClientKey PASTE-THE-KEY-HERE
```

It writes `FoghornClient.mst` next to the MSI. The script uses the Windows
Installer COM API that is part of Windows — no WiX, SDK or other tools needed.

**Test the transform on your pilot PC.** First uninstall the manual install
from Step 1 with `Uninstall-FoghornClient.cmd`, then from an admin prompt:

```bat
msiexec /i FoghornClient.msi TRANSFORMS=FoghornClient.mst /qb
```

`--status` should still say `Connection test: OK`.

> **Two paths, one job.** Step 1's `.cmd` installer and the MSI do the same
> thing on a single PC. The MSI is what Group Policy Software Installation
> understands; the `.cmd` is what a start-up script can run. Pilot with either.

## Step 3 — Put the files on a share

Group Policy Software Installation reads the MSI over the network at install
time, so it must live somewhere every domain computer can read. The domain's
`NETLOGON` share is the simplest — every domain computer can already read it.

1. On a domain controller (or from your PC as a domain admin) open
   `\\YOURDOMAIN\NETLOGON` and create a folder called `Foghorn`.
2. Copy the MSI **and** the MST into it:

   ```
   \\YOURDOMAIN\NETLOGON\Foghorn\FoghornClient.msi
   \\YOURDOMAIN\NETLOGON\Foghorn\FoghornClient.mst
   ```

Any other share works too, provided **Domain Computers** has Read access to
both the share and the folder.

## Step 4 — Create the Group Policy Object

1. Open **Group Policy Management** (`gpmc.msc`).

2. Find the OU that contains the **computers** that should get the client
   (not the users). Right-click it → **Create a GPO in this domain, and Link
   it here…** → name it `Foghorn client` → **OK**.

   > Start with a small OU — one classroom — and widen it once you are happy.

3. Right-click the new GPO → **Edit**.

4. Go to **Computer Configuration → Policies → Software Settings → Software
   installation**. Right-click in the pane → **New → Package…**.

5. Type the **UNC path** to the MSI directly (do not browse to a mapped drive):

   ```
   \\YOURDOMAIN\NETLOGON\Foghorn\FoghornClient.msi
   ```

6. Choose **Advanced** (not Assigned or Published) so the next dialog lets you
   attach a transform. Click **OK**.

7. Go to the **Modifications** tab. Click **Add…** and pick
   `FoghornClient.mst` from the same folder. Click **OK**.

That is the whole deployment. Each PC installs the client the next time it
starts — Group Policy runs Software Installation before anyone logs on.

## Step 5 — Check it worked

On one PC in the OU:

1. From an administrator prompt, run `gpupdate /force` and then **restart**
   (choose *Restart*, not *Shut down* — see the note below).
2. After it boots and someone logs on, from an admin prompt:

   ```bat
   dir "C:\Program Files\Foghorn"
   reg query HKLM\SOFTWARE\Foghorn
   ```

   You should see `FoghornClient.exe`, and `ServerUrl` and `ClientKey` in the
   registry.
3. As a normal user, `"C:\Program Files\Foghorn\FoghornClient.exe" --status`
   should end with `Connection test: OK`.
4. The PC appears in the console under **Computers**.

> **Shut down vs restart.** Windows 10/11 "Fast Startup" makes *Shut down →
> power on* a kind of hibernate-resume; Software Installation and start-up
> scripts do not run then. They always run on **Restart**, and on a cold boot
> if Fast Startup is off. Many schools disable Fast Startup by policy
> (*Computer Configuration → Administrative Templates → System → Shutdown →
> Require use of fast startup* = **Disabled**). Either way every PC gets the
> client the next time it properly restarts — Windows Update will see to that.

If a PC does not get it, go to
[TROUBLESHOOTING.md](TROUBLESHOOTING.md#a-pc-never-appears-in-the-console).

## Upgrading, rotating the key, removing

### Upgrading to a new client version

**From 1.2, PCs can update from the web console** (Updates → roll out). See
[UPDATING.md](UPDATING.md), including how that fits with a Group Policy
deployment. To upgrade through Group Policy instead:

1. Copy the new `FoghornClient.msi` over the old one on the share.
2. In the GPO editor, right-click the package → **All Tasks → Redeploy
   application**.
3. PCs pick up the upgrade at their next restart.

For a smooth upgrade, the new MSI needs a higher `ProductVersion` than the old
one. Foghorn does this at release time — you do not have to.

### Rotating the client key

**Best way — use the ADMX policy** ([Appendix A](#appendix-a-setting-the-server-address-with-the-group-policy-template-admx)).
Change the value once; every PC picks it up at its next Group Policy refresh
(about 90 minutes) with no restart or reinstall.

**With the transform alone**, changing the key means: generate a new MST,
replace the file on the share, and redeploy. Every PC then reinstalls at its
next restart. Workable, but noisier than the ADMX method.

### Removing from every PC

In the GPO editor, right-click the package → **All Tasks → Remove**, choose
*Immediately uninstall the software from users and computers*. Each PC removes
it at its next restart. Delete the GPO once they have all reported clean.

### Removing from one PC

Control Panel → *Programs and Features* → *Foghorn Client* → **Uninstall**. Or:

```powershell
powershell -ExecutionPolicy Bypass -File .\Uninstall-FoghornClient.ps1
```

## Appendix A — Setting the server address with the Group Policy template (ADMX)

Advantages over settings from the MST: values show in GPO reports, changes
reach PCs at the next policy refresh (about 90 minutes, or `gpupdate`) with no
restart or reinstall, and they take priority over anything stored locally.

You still need Steps 3 and 4 to install the program itself — you can generate
the MST **without a client key** (it will refuse; use a placeholder for now)
and set the real values in the ADMX policy instead.

1. Copy the template into your policy store:

   | Copy this | To here (central store) |
   |---|---|
   | `deploy\gpo\Foghorn.admx` | `\\YOURDOMAIN\SYSVOL\YOURDOMAIN\Policies\PolicyDefinitions\` |
   | `deploy\gpo\en-US\Foghorn.adml` | `\\YOURDOMAIN\SYSVOL\YOURDOMAIN\Policies\PolicyDefinitions\en-US\` |

   No central store? Copy them to `C:\Windows\PolicyDefinitions\` (and its
   `en-US` subfolder) on the machine where you edit GPOs.

2. Edit the `Foghorn client` GPO. Go to **Computer Configuration → Policies →
   Administrative Templates → Foghorn**.

3. Open **Foghorn server connection**, choose **Enabled**, fill in **Server
   address** and **Client key**, then **OK**.

The other setting, **Connect through the system web proxy**, should stay *Not
configured* unless PCs can only reach the Foghorn server through a proxy.

## Appendix B — Start-up script instead of MSI

If you would rather not use Software Installation, a computer start-up script
works too. It is simpler for very small deployments and the MSI's `msi`
plumbing is out of the picture.

1. Copy these three files to `\\YOURDOMAIN\NETLOGON\Foghorn\`:
   - `dist\FoghornClient.exe`
   - `deploy\Install-FoghornClient.ps1`
   - `deploy\Install-FoghornClient.cmd`

2. In the GPO editor: **Computer Configuration → Policies → Windows Settings →
   Scripts (Startup/Shutdown)** → double-click **Startup** → stay on the first
   tab (**Scripts**, not "PowerShell Scripts") → **Add…**.

3. Fill in:

   | Field | Value |
   |---|---|
   | **Script Name** | `\\YOURDOMAIN\NETLOGON\Foghorn\Install-FoghornClient.cmd` |
   | **Script Parameters** | `-ServerUrl http://foghorn01:8080 -ClientKey PASTE-THE-KEY` |

4. In the same GPO, set **Computer Configuration → Policies → Administrative
   Templates → System → Logon → Always wait for the network at computer
   startup and logon** to **Enabled**, so the network is up before the script
   runs.

The script is idempotent — it does nothing if nothing needs doing, so it costs
under a second per boot. **Upgrading** means replacing the `.exe` on the share.
**Changing the key** means editing the Script Parameters. The script installer
also adds a watchdog scheduled task that restarts the client if it is closed
(see [SECURITY.md](SECURITY.md#students-closing-the-client)); the MSI does
not.

To remove: copy `Uninstall-FoghornClient.cmd` and `Uninstall-FoghornClient.ps1`
to the share and replace the start-up script with the uninstall one, until
every PC has restarted.

## Appendix C — Intune, PDQ Deploy, SCCM and similar

**With the MSI + MST**:

```
msiexec /i FoghornClient.msi TRANSFORMS=FoghornClient.mst /qn
```

**With the MSI on its own**, passing settings on the command line:

```
msiexec /i FoghornClient.msi /qn SERVERURL=http://foghorn01:8080 CLIENTKEY=PASTE-THE-KEY
```

**With the script installer**, deployed as a package that runs as SYSTEM:

```
Install-FoghornClient.cmd -ServerUrl http://foghorn01:8080 -ClientKey PASTE-THE-KEY
```

Detection rule: `C:\Program Files\Foghorn\FoghornClient.exe` exists. Exit code
`0` is success; on failure look in `C:\Windows\Temp\FoghornClient-install.log`
(script) or the MSI log if you enabled one.

## Appendix D — Reference: what the installer changes, settings, command line

### What lands on a PC

| Change | Detail |
|---|---|
| Program | `C:\Program Files\Foghorn\FoghornClient.exe` |
| Settings (only if you passed them, or the MST set them) | `HKLM\SOFTWARE\Foghorn` → `ServerUrl`, `ClientKey`, optionally `UseSystemProxy` |
| Start at log-on | `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Run` → `FoghornClient` |
| Updater service *(MSI, from 1.2)* | **Foghorn Updater** (`FoghornUpdater`): `FoghornClient.exe --service` as LocalSystem, automatic start. Installs releases an administrator rolls out from the console ([UPDATING.md](UPDATING.md)). Removed with the MSI. |
| Watchdog *(script installer only)* | Scheduled task **Foghorn Client Watchdog**: runs as the logged-on user at log-on and every 5 minutes; does nothing if the client is already running. |
| Install log *(script installer only)* | `C:\Windows\Temp\FoghornClient-install.log` |

Per user, the client writes a small log to `%LOCALAPPDATA%\Foghorn\client.log`.

### Where the client looks for its settings

First match wins:

1. The command line: `--server URL --key KEY`
2. Group Policy: `HKLM\SOFTWARE\Policies\Foghorn` (Appendix A)
3. The installer's registry key: `HKLM\SOFTWARE\Foghorn`
4. A file called `foghorn-client.ini` next to the `.exe`:

   ```ini
   ServerUrl=http://foghorn01:8080
   ClientKey=3f9a1c…
   ```

The client re-reads its settings every time it checks in with the server (at
least twice a minute), so a change never needs a log-off.

### Client command line

| Command | What it does |
|---|---|
| `FoghornClient.exe` | Normal running. No window. A second copy in the same session exits immediately. |
| `FoghornClient.exe --test` | Shows one alert of each style. Needs no server. |
| `FoghornClient.exe --status` | Shows the settings in use, what the PC reports about itself, and tests the connection. |
| `FoghornClient.exe --server URL --key KEY` | Runs against a different server, for testing. |
| `FoghornClient.exe --update-now` | As administrator: asks the updater service to check for an update straight away. |
| `FoghornClient.exe --test-lock` | Locks this screen for 10 seconds, to see what a lock looks like. |
