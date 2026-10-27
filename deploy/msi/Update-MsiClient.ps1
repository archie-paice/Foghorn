<#
.SYNOPSIS
    Puts the current dist\FoghornClient.exe inside dist\FoghornClient.msi, on
    Windows, using only what comes with Windows.

.DESCRIPTION
    The MSI carries the client in an embedded cabinet. Replacing the exe in
    dist\ does NOT change what the MSI installs - which is how 1.0.1 came to
    ship an installer that still deployed the broken client.

    build-msi.sh rebuilds the whole MSI from FoghornClient.wxs, but it needs
    wixl and msitools, so it only runs on Linux. This script does the part that
    goes stale - the payload - with makecab and the Windows Installer COM API,
    both of which are part of Windows. Use it when you have rebuilt the client
    on Windows and want the MSI to match.

    It then runs the same checks build-msi.sh does, including unpacking the
    finished MSI and comparing what it would install, and refreshes
    dist\SHA256SUMS.txt.

    It also keeps the version in step. FoghornClient.wxs is where the version
    is written down; if the MSI disagrees, this script raises it and mints a new
    ProductCode, because Windows Installer will not replace an installed product
    with one carrying the same ProductCode and version - PCs would simply never
    pick the new build up. The UpgradeCode is left alone, which is what lets
    MajorUpgrade remove the old client at the next restart.

    Structural changes to FoghornClient.wxs (components, registry values,
    directories) are beyond it - rebuild with build-msi.sh on Linux.

.PARAMETER MsiPath
    The MSI to update. Default: dist\FoghornClient.msi

.PARAMETER ExePath
    The client to put inside it. Default: dist\FoghornClient.exe

.EXAMPLE
    client\build.cmd
    deploy\msi\Update-MsiClient.ps1
#>
[CmdletBinding()]
param(
    [string]$MsiPath,
    [string]$ExePath
)

$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
if (-not $MsiPath) { $MsiPath = Join-Path $root 'dist\FoghornClient.msi' }
if (-not $ExePath) { $ExePath = Join-Path $root 'dist\FoghornClient.exe' }
foreach ($p in @($MsiPath, $ExePath)) { if (-not (Test-Path $p)) { throw "Cannot find $p" } }
$MsiPath = (Resolve-Path $MsiPath).Path
$ExePath = (Resolve-Path $ExePath).Path

function Get-Rows($db, $sql, [int]$cols) {
    # $cols is passed in because Record.FieldCount does not come back reliably
    # through PowerShell's COM binding - it returns nothing, and every row then
    # reads as empty.
    $view = $db.OpenView($sql)
    [void]$view.Execute($null)
    $rows = @()
    while ($true) {
        $rec = $view.Fetch()
        if (-not $rec) { break }
        $row = @()
        for ($i = 1; $i -le $cols; $i++) { $row += [string]$rec.StringData($i) }
        $rows += , $row
    }
    [void]$view.Close()
    # The comma stops PowerShell unwrapping a single row into its own fields.
    return , $rows
}
function Assert($cond, $msg) {
    if (-not $cond) { throw "CHECK FAILED: $msg" }
    Write-Host "  ok  $msg"
}

$installer = New-Object -ComObject WindowsInstaller.Installer
# Read only: the MSI is rebuilt into a fresh database below rather than edited
# in place (see "rebuild the package").
$db = $installer.OpenDatabase($MsiPath, 0)

# The name inside the cabinet is the File table's key, not the name on disk.
$fileRows = Get-Rows $db "SELECT File, FileName FROM File" 2
if ($fileRows.Count -ne 1) {
    throw "Expected exactly one file in the MSI, found $($fileRows.Count). Rebuild with build-msi.sh instead."
}
$fileKey = $fileRows[0][0]
$cabName = ((Get-Rows $db "SELECT Cabinet FROM Media" 1)[0][0]) -replace '^#', ''   # '#foghorn.cab' = embedded
$newSize = (Get-Item $ExePath).Length

# FoghornClient.wxs is the one place the version is written down. If the MSI
# disagrees with it, this script brings the MSI up to date rather than letting
# the two drift.
$wxsPath = Join-Path $PSScriptRoot 'FoghornClient.wxs'
if (-not (Test-Path $wxsPath)) { throw "Cannot find $wxsPath" }
$wxsXml = [xml](Get-Content $wxsPath -Raw)
$wxsVersion = $wxsXml.Wix.Product.Version
if (-not $wxsVersion) { throw "No Product Version in $wxsPath" }
$msiVersion = (Get-Rows $db "SELECT Value FROM Property WHERE Property = 'ProductVersion'" 1)[0][0]

Write-Host "MSI:      $MsiPath"
Write-Host "Client:   $ExePath ($newSize bytes)"
Write-Host "Cabinet:  $cabName, holding '$fileKey'"
Write-Host "Version:  $msiVersion in the MSI, $wxsVersion in FoghornClient.wxs"

$work = Join-Path ([System.IO.Path]::GetTempPath()) ("foghorn-msi-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force $work | Out-Null
try {
    # ---- build a replacement cabinet --------------------------------------
    $ddf = Join-Path $work 'payload.ddf'
    @"
.OPTION EXPLICIT
.Set CabinetNameTemplate=$cabName
.Set DiskDirectoryTemplate=$work
.Set InfFileName=$work\payload.inf
.Set RptFileName=$work\payload.rpt
.Set Cabinet=on
.Set Compress=on
.Set CompressionType=MSZIP
.Set MaxDiskSize=0
.Set UniqueFiles=off
"$ExePath" $fileKey
"@ | Set-Content $ddf -Encoding ASCII

    $makecab = Join-Path $env:WINDIR 'system32\makecab.exe'
    $p = Start-Process $makecab -ArgumentList @('/f', "`"$ddf`"") -Wait -PassThru -WindowStyle Hidden `
             -RedirectStandardOutput "$work\makecab.out" -RedirectStandardError "$work\makecab.err"
    $newCab = Join-Path $work $cabName
    if ($p.ExitCode -ne 0 -or -not (Test-Path $newCab)) {
        Get-Content "$work\makecab.out", "$work\makecab.err" -ErrorAction SilentlyContinue
        throw "makecab failed (exit $($p.ExitCode))"
    }
    Write-Host "Rebuilt cabinet ($((Get-Item $newCab).Length) bytes)"

    # ---- rebuild the package from its own tables ----------------------------
    # Editing the wixl-built MSI in place corrupted it. Its string pool
    # under-counts strings that several rows share. In 1.0.2 "Foghorn" was
    # both the Manufacturer and the install folder's name. When 1.0.3 changed
    # the Manufacturer, the count reached zero and the folder lost its name, so
    # msiexec installed into "C:\Program Files\#_PatchCache\" (1.0.3) and
    # "...\PatchId\" (1.1.0) instead of "...\Foghorn\".
    #
    # Exporting every table and importing it into a new database makes Windows
    # Installer build the string pool itself, with correct counts. Every later
    # edit is safe.
    $idt = Join-Path $work 'idt'
    New-Item -ItemType Directory -Force $idt | Out-Null
    $tables = @(foreach ($r in (Get-Rows $db "SELECT Name FROM _Tables" 1)) { $r[0] })
    $db.Export('_ForceCodepage', $idt, '_ForceCodepage.idt')
    $db.Export('_SummaryInformation', $idt, '_SummaryInformation.idt')
    foreach ($t in $tables) { $db.Export($t, $idt, "$t.idt") }
    [void][System.Runtime.InteropServices.Marshal]::ReleaseComObject($db)
    $db = $null

    # Folder names come from FoghornClient.wxs. That repairs the ones the
    # corruption above already blanked out of earlier MSIs, and it has to happen
    # in the exported file: a new database will not take a blank DefaultDir.
    $names = @{}
    foreach ($dir in $wxsXml.SelectNodes('//*[local-name()="Directory"][@Name]')) { $names[$dir.Id] = $dir.Name }
    $dirIdt = Join-Path $idt 'Directory.idt'
    $lines = Get-Content $dirIdt
    for ($i = 3; $i -lt $lines.Count; $i++) {   # after the three header lines
        $cols = $lines[$i] -split "`t", -1
        if ($names.ContainsKey($cols[0]) -and $cols[2] -ne $names[$cols[0]]) {
            Write-Host "Repaired: folder $($cols[0]) was '$($cols[2])', now '$($names[$cols[0]])'"
            $cols[2] = $names[$cols[0]]
            $lines[$i] = $cols -join "`t"
        }
    }
    Set-Content $dirIdt $lines -Encoding ASCII

    $rebuilt = Join-Path $work 'rebuilt.msi'
    $db = $installer.OpenDatabase($rebuilt, 3)   # 3 = msiOpenDatabaseModeCreate
    $db.Import($idt, '_ForceCodepage.idt')
    foreach ($t in $tables) { $db.Import($idt, "$t.idt") }
    $db.Import($idt, '_SummaryInformation.idt')

    # ---- put the cabinet in and correct the recorded size -------------------
    # _Streams will not take a stream as a query parameter, so build the row
    # and insert it. 1 = msiViewModifyInsert.
    $view = $db.OpenView("SELECT Name, Data FROM _Streams")
    [void]$view.Execute($null)
    $rec = $installer.CreateRecord(2)
    $rec.StringData(1) = $cabName
    $rec.SetStream(2, $newCab)
    [void]$view.Modify(1, $rec)
    [void]$view.Close()

    $view = $db.OpenView("UPDATE File SET FileSize = $newSize WHERE File = '$fileKey'")
    [void]$view.Execute($null)
    [void]$view.Close()

    # ---- bring the version into line, if it has moved ---------------------
    if ($wxsVersion -ne $msiVersion) {
        # A new version needs a new ProductCode, or Windows Installer treats it
        # as the same product already installed and PCs never pick it up. The
        # UpgradeCode stays put - that is what lets MajorUpgrade replace the
        # old one at next restart.
        $newProductCode = '{' + [guid]::NewGuid().ToString().ToUpper() + '}'
        foreach ($set in @("Value = '$wxsVersion' WHERE Property = 'ProductVersion'",
                           "Value = '$newProductCode' WHERE Property = 'ProductCode'")) {
            $view = $db.OpenView("UPDATE Property SET $set")
            [void]$view.Execute($null)
            [void]$view.Close()
        }
        Write-Host "Version:  raised to $wxsVersion, new ProductCode $newProductCode"
        $msiVersion = $wxsVersion
    }
    # 9 = PID_REVNUMBER, the package code. Every distinct MSI needs its own, and
    # this one carries a different client whether or not the version moved.
    $si = $db.SummaryInformation(1)
    $si.Property(9) = '{' + [guid]::NewGuid().ToString().ToUpper() + '}'
    $si.Persist()
    [void][System.Runtime.InteropServices.Marshal]::ReleaseComObject($si)

    # ---- bring the plain properties into line -----------------------------
    # Manufacturer is the Publisher column in Apps and Features; the ARP*
    # properties sit beside the entry there. They are ordinary Property rows, so
    # they can be carried across without rebuilding the whole package.
    $wanted = [ordered]@{ 'Manufacturer' = $wxsXml.Wix.Product.Manufacturer }
    foreach ($prop in $wxsXml.Wix.Product.Property) {
        if ($prop.Value) { $wanted[$prop.Id] = $prop.Value }
    }
    foreach ($name in $wanted.Keys) {
        $value = [string]$wanted[$name]
        $existing = Get-Rows $db "SELECT Value FROM Property WHERE Property = '$name'" 1
        $sqlValue = $value -replace "'", "''"
        if ($existing.Count -eq 1) {
            if ($existing[0][0] -eq $value) { continue }
            $view = $db.OpenView("UPDATE Property SET Value = '$sqlValue' WHERE Property = '$name'")
        } else {
            $view = $db.OpenView("INSERT INTO Property (Property, Value) VALUES ('$name', '$sqlValue')")
        }
        [void]$view.Execute($null)
        [void]$view.Close()
        Write-Host "Property: $name = $value"
    }

    $db.Commit()
    [void][System.Runtime.InteropServices.Marshal]::ReleaseComObject($db)
    $db = $null
    [GC]::Collect(); [GC]::WaitForPendingFinalizers()
    Copy-Item $rebuilt $MsiPath -Force
    Write-Host "Rebuilt the package from its tables (fresh string pool)"
}
finally {
    # Every COM handle has to go before the MSI can be opened again, or the
    # checks below fail with a bare COMException on OpenDatabase.
    foreach ($o in 'rec', 'view', 'db') {
        $val = Get-Variable $o -ValueOnly -ErrorAction SilentlyContinue
        if ($val) { [void][System.Runtime.InteropServices.Marshal]::ReleaseComObject($val) }
        Set-Variable $o $null
    }
    [GC]::Collect(); [GC]::WaitForPendingFinalizers()
    Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue
}

# ---- checks: fail rather than leave a broken installer in dist\ -----------
Write-Host ""
Write-Host "Checking the result..."
# A fresh Installer object: the one used for the transacted write does not
# reliably serve queries afterwards.
$verifier = New-Object -ComObject WindowsInstaller.Installer
$vdb = $verifier.OpenDatabase($MsiPath, 0)   # 0 = read only

# Assign first: "foreach ($x in Get-Rows ...)" hands the whole row-set over as
# a single item instead of enumerating the rows.
$propRows = Get-Rows $vdb "SELECT Property, Value FROM Property" 2
$props = @{}
foreach ($r in $propRows) { $props[$r[0]] = $r[1] }
$version = $props['ProductVersion']

$secure = [string]$props['SecureCustomProperties']
Assert ($secure -match 'SERVERURL' -and $secure -match 'CLIENTKEY' -and $secure -match 'USESYSTEMPROXY') `
       'SERVERURL, CLIENTKEY and USESYSTEMPROXY are secure custom properties'

$settings = Get-Rows $vdb "SELECT Component, Condition FROM Component WHERE Component = 'Settings'" 2
Assert ($settings.Count -eq 1 -and $settings[0][1] -eq 'SERVERURL') 'Settings component is conditioned on SERVERURL'

$regRows = Get-Rows $vdb "SELECT Name FROM Registry" 1
$regNames = @(foreach ($r in $regRows) { $r[0] })
foreach ($v in 'ServerUrl', 'ClientKey', 'UseSystemProxy') {
    Assert ($regNames -contains $v) "registry value $v is present"
}
Assert ((Get-Rows $vdb "SELECT FileSize FROM File WHERE File = '$fileKey'" 1)[0][0] -eq "$newSize") `
       "File table records $newSize bytes"
Assert ($version -eq $wxsVersion) "ProductVersion is $wxsVersion, matching FoghornClient.wxs"
Assert ($props['Manufacturer'] -eq $wxsXml.Wix.Product.Manufacturer) `
       "Manufacturer (the Publisher shown in Apps and Features) is $($wxsXml.Wix.Product.Manufacturer)"
[void][System.Runtime.InteropServices.Marshal]::ReleaseComObject($vdb)
[void][System.Runtime.InteropServices.Marshal]::ReleaseComObject($verifier)
$vdb = $null; $verifier = $null
[GC]::Collect(); [GC]::WaitForPendingFinalizers()

# The check that matters: unpack the MSI the way msiexec would, and compare.
$extract = Join-Path ([System.IO.Path]::GetTempPath()) ("foghorn-verify-" + [guid]::NewGuid().ToString('N'))
try {
    $p = Start-Process msiexec.exe -ArgumentList @('/a', "`"$MsiPath`"", '/qn', "TARGETDIR=`"$extract`"") -Wait -PassThru
    if ($p.ExitCode -ne 0) { throw "could not unpack the MSI to check it (msiexec exit $($p.ExitCode))" }
    $placed = Get-ChildItem $extract -Recurse -Filter 'FoghornClient.exe' | Select-Object -First 1
    if (-not $placed) { throw "the unpacked MSI contains no FoghornClient.exe" }
    # Not just "somewhere": 1.0.3 shipped an MSI that put the client in
    # "#_PatchCache\" and still passed a check that only looked for the file.
    $folder = $wxsXml.SelectSingleNode('//*[local-name()="Directory"][@Id="INSTALLDIR"]').Name
    Assert ($placed.Directory.Name -eq $folder) "the client installs into a folder called $folder (got '$($placed.Directory.Name)')"
    Assert ((Get-FileHash $placed.FullName -Algorithm SHA256).Hash -eq (Get-FileHash $ExePath -Algorithm SHA256).Hash) `
           'the client the MSI installs is byte-for-byte the one in dist\'
}
finally {
    Remove-Item $extract -Recurse -Force -ErrorAction SilentlyContinue
}

# ---- keep the checksums in step ------------------------------------------
# Only for the real dist\ MSI; a test run against a copy elsewhere must not
# rewrite the shipped checksums.
$dist = Join-Path $root 'dist'
$isDist = ($MsiPath -eq (Join-Path $dist 'FoghornClient.msi')) -and ($ExePath -eq (Join-Path $dist 'FoghornClient.exe'))
if ($isDist) {
    $lines = foreach ($n in 'FoghornClient.exe', 'FoghornClient.msi', 'foghorn-server.exe', 'foghorn-server-linux-amd64') {
        $f = Join-Path $dist $n
        if (Test-Path $f) { "{0}  {1}" -f (Get-FileHash $f -Algorithm SHA256).Hash.ToLower(), $n }
    }
    Set-Content (Join-Path $dist 'SHA256SUMS.txt') ($lines -join "`n") -Encoding ASCII
    Write-Host "  ok  dist\SHA256SUMS.txt refreshed"
}

Write-Host ""
if ($isDist) {
    Write-Host "OK: $MsiPath ($version) installs the client in dist\; SHA256SUMS.txt is up to date." -ForegroundColor Green
} else {
    Write-Host "OK: $MsiPath ($version) installs $ExePath. (SHA256SUMS.txt left alone - not the dist\ MSI.)" -ForegroundColor Green
}
