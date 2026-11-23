# Building from source

You do not need to build anything to use Foghorn — `dist\` has ready-made
programs. This is for when you want to change something.

## The client (`client\`) — builds on any Windows PC, nothing to install

The client is C# 5 for .NET Framework 4.x, deliberately: the compiler for that
ships inside Windows.

```bat
cd client
build.cmd
```

writes `dist\FoghornClient.exe` directly — there is no copying step, on purpose.
Then put that client inside the MSI and refresh the checksums:

```bat
deploy\msi\Update-MsiClient.ps1
```

**Do both in the same commit that changes `client\src`.** 1.0.1 changed the
source, shipped the old binary, and nothing noticed — and even once the exe is
right, the MSI keeps its *own* copy of the client in an embedded cabinet, so it
goes stale separately. The *Client build test* workflow now checks all three
(source, `dist\` and the MSI's payload) and fails the pull request if they
disagree.

To check before you push, build to a scratch folder and compare:

```powershell
client\build.cmd "$env:TEMP\check\FoghornClient.exe"
client\Get-AssemblyContentHash.ps1 -Path "$env:TEMP\check\FoghornClient.exe"
client\Get-AssemblyContentHash.ps1 -Path dist\FoghornClient.exe   # must match
```

Keep the file name `FoghornClient.exe`: the compiler records it inside the
assembly, so a build under any other name never matches. A plain SHA256 will not
do either — `csc.exe` stamps a build time and a new MVID into every build, so
two builds of identical source always differ. `Get-AssemblyContentHash.ps1`
blanks those two fields and hashes the rest.

Because it has to build with that older compiler, the source avoids newer C#
syntax (`$"…"` strings, `?.`, `nameof`, `out var`, expression-bodied members).
If you open it in Visual Studio and let it "modernise" the code, `build.cmd`
will stop working — build it as a normal .NET Framework 4.8 WinForms project
instead and that is fine too.

### Do not build the shipped client with Mono

Mono can compile the client on Linux or macOS, and for a long time this file
told you to build `dist/FoghornClient.exe` that way. **Do not.** It is how the
1.0.1 client came to fail on every Windows PC.

`mcs` compiles against Mono's class library, which has methods .NET Framework
has never had. Nothing warns you: the build succeeds, the exe looks fine, and it
only fails on a real PC, at run time, the first time it reaches that line. In
1.0.1 the method was `String.TrimEnd(char)` — present in Mono and in .NET Core,
absent from .NET Framework at every version — and every poll on every PC died
with:

```
Method not found: 'System.String System.String.TrimEnd(Char)'
```

If you want to compile on Linux to check the code still parses, build it to a
throwaway path and never to `dist/`:

```bash
mcs -target:winexe -platform:anycpu -optimize+ -langversion:5 -out:/tmp/syntax-check.exe \
    -win32icon:client/foghorn.ico -r:System.dll -r:System.Core.dll -r:System.Drawing.dll \
    -r:System.Windows.Forms.dll -r:System.Web.Extensions.dll client/src/*.cs
```

The binary that ships is built on Windows by `client\build.cmd`, with the
compiler that comes with .NET Framework. That compiler only offers the methods
the target PCs actually have, so this class of fault cannot compile in the first
place. The *Client build test* workflow enforces it: it rebuilds on Windows,
checks `dist\` matches, and then runs the shipped exe against a real server, so
a Mono-built binary fails the pull request instead of reaching PCs.

| File | What is in it |
|---|---|
| `src\Program.cs` | Start-up, command line, settings, who-am-I (OU, groups), logging. |
| `src\Poller.cs` | Talking to the server; retry and back-off. |
| `src\AlertForm.cs` | What alerts look like (`Look`), the two window styles, and the manager that stacks and queues them. |
| `src\Native.cs` | The few Windows API calls. |

## The server (`server\`) — needs Go 1.22 or later

Install Go from <https://go.dev/dl/>. Then, on Windows:

```bat
cd server
go test ./...
go build -mod=vendor -trimpath -ldflags "-s -w" -o ..\dist\foghorn-server.exe .
```

From Linux/macOS, for Windows: prefix the build with `GOOS=windows GOARCH=amd64`.

`-mod=vendor` matters. The one third-party dependency (`golang.org/x/sys`, for
the Windows service) is included in `server\vendor\`, and `go.mod` contains a
`replace` line pointing at a folder that only existed on the original build
machine. With `-mod=vendor` Go never looks for that folder. If you would rather
tidy it up on a machine with internet access: delete the `replace` line and the
`vendor` folder, then run `go mod tidy`.

### The Docker image

`server/Dockerfile` builds the server inside a `golang:1.22-alpine` container
(again with `-mod=vendor`, so it needs no internet access for modules) and
copies the result into a small Alpine image. From the repository root:

```bash
docker compose build               # or: docker build -t foghorn-server server
```

The image sets `FOGHORN_IN_CONTAINER=1`. That switches off **Update server** in
the console, and makes `reset-password` give `docker compose` commands. Nothing
in `dist/` is used or changed by the image build.

The web console is plain HTML, CSS and JavaScript in `server\web\` — no build
step, no npm. It is embedded into the `.exe` when you build, so rebuild the
server after changing it.

| File | What is in it |
|---|---|
| `main.go` | Command line, config, first-run set-up, URL routes, start and stop. |
| `store.go` | The data model and saving to JSON files. |
| `api_client.go` | The endpoint PCs check in to; delivery and recall logic. |
| `api_admin.go` | Everything the web console and API tokens call. |
| `auth.go` | Passwords, sessions, tokens, lock-out, the permission check. |
| `match.go` | Targeting rules. |
| `service_windows.go` | Windows service install/run. |
| `foghorn_test.go` | Tests. Run them before and after any change. |

## The MSI and its transform (`deploy\msi\`)

On Linux, install `wixl` and `msitools` (`sudo apt install wixl msitools`) and run:

```bash
deploy/msi/build-msi.sh
```

**Always use the script, not a bare `wixl` build.** wixl cannot express two
things the MSI needs, so the script applies them after the build:

1. `Condition = SERVERURL` on the `Settings` component — installing without a
   transform then writes nothing to `HKLM\SOFTWARE\Foghorn` that would mask
   the Group Policy settings.
2. `SERVERURL;CLIENTKEY;USESYSTEMPROXY` in `SecureCustomProperties`, so those
   properties survive a managed install.

It then checks the result (version, both tweaks, all three registry values, and
that the embedded `FoghornClient.exe` matches `dist\`) and refreshes
`dist/SHA256SUMS.txt`. It fails loudly rather than leave a broken MSI in
`dist/`. Rebuild the client exe first if it changed.

**Testing it.** The *MSI deploy test* GitHub Actions workflow
(`.github/workflows/msi-deploy-test.yml`) installs the MSI on a real Windows
runner on every pull request that touches it: it generates a transform with
`New-FoghornTransform.ps1`, then checks a plain install, an install with the
transform, a repair, uninstall, and command-line properties. The msiexec logs
are attached to each run. You can also start it by hand from the Actions tab.

On Windows with WiX Toolset v3: `candle -arch x64 FoghornClient.wxs` then
`light FoghornClient.wixobj`. In WiX source you would express the same two
tweaks as `<Condition>SERVERURL</Condition>` inside the `Settings` component
and `<Property Id="SecureCustomProperties" Value="…" />` — wixl does not accept
either. Bump `Version` in the `.wxs` for each release so upgrades work.

The transform (`.mst`) is generated on Windows from the base MSI by
`New-FoghornTransform.ps1`, using the built-in `WindowsInstaller.Installer`
COM object (`GenerateTransform` + `CreateTransformSummaryInfo`). This is the
same API WiX's `MakeMST` uses. It does not need to run on the machine that
built the MSI — anyone deploying can regenerate a transform whenever the
client key rotates.

## Calling it something other than "Foghorn"

The name people see comes from two places: the **organisation name** in
Settings (top of every alert) — no rebuild needed — and the word "Foghorn" in
the console's sidebar and page title (`server\web\app.js`, `index.html`).
Renaming the programs, service, registry keys and install folders is a
search-and-replace across `server\`, `client\` and `deploy\`; keep the registry
paths in `client\src\Program.cs`, `deploy\gpo\Foghorn.admx` and the install
scripts in step with each other.
