using System;
using System.Collections;
using System.Collections.Generic;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Net;
using System.Runtime.InteropServices;
using System.Security.Cryptography;
using System.ServiceProcess;
using System.Text;
using System.Threading;
using System.Web.Script.Serialization;
using Microsoft.Win32;

namespace Foghorn
{
    /// <summary>
    /// "Foghorn Updater": a Windows service, installed by the MSI, that keeps
    /// the client up to date. It is FoghornClient.exe started with --service,
    /// running as SYSTEM, because only SYSTEM can replace a program in
    /// C:\Program Files.
    ///
    /// Every 20 minutes or so it asks the Foghorn server whether this PC should
    /// update. The server only names a version. The MSI is downloaded from this
    /// project's GitHub releases, from a repository built into this program,
    /// over HTTPS, and checked against the SHA-256 the server sent. Only if
    /// GitHub cannot be reached, and only when the Foghorn server itself is on
    /// HTTPS, is the server's copy used instead.
    /// </summary>
    class UpdaterService : ServiceBase
    {
        public const string Name = "FoghornUpdater";
        /// <summary>Custom service control: "check for an update now" (FoghornClient.exe --update-now).</summary>
        public const int CheckNowCommand = 129;
        Thread worker;
        readonly ManualResetEvent stopping = new ManualResetEvent(false);
        readonly AutoResetEvent checkNow = new AutoResetEvent(false);

        public UpdaterService()
        {
            ServiceName = Name;
            CanStop = true;
            CanShutdown = true;
            AutoLog = false;
        }

        protected override void OnStart(string[] args)
        {
            worker = new Thread(delegate() { Updater.Run(stopping, checkNow); });
            worker.IsBackground = true;
            worker.Name = "Foghorn updater";
            worker.Start();
        }

        protected override void OnStop() { stopping.Set(); }
        protected override void OnShutdown() { stopping.Set(); }
        protected override void OnCustomCommand(int command) { if (command == CheckNowCommand) checkNow.Set(); }
    }

    static class Updater
    {
        /// <summary>Where updates come from. A fork changes it here, or with the UpdateRepo policy value.</summary>
        const string DefaultRepo = "archie-paice/Foghorn";

        static string state = "idle", detail = "";

        /// <summary>The service loop: check soon after start, then every so often.</summary>
        public static void Run(WaitHandle stopping, WaitHandle checkNow)
        {
            Poller.Init();
            var random = new Random();
            FinishPendingInstall();
            int waitMs = random.Next(20000, 90000); // spread a room's PCs out after a power cut
            while (WaitHandle.WaitAny(new[] { stopping, checkNow }, waitMs) != 0)
            {
                int checkSeconds = 1200;
                try { checkSeconds = CheckOnce(stopping); }
                catch (Exception ex) { Write("Update check failed: " + ex.Message); }
                checkSeconds = Math.Max(300, Math.Min(6 * 3600, checkSeconds));
                waitMs = (checkSeconds + random.Next(0, checkSeconds / 4)) * 1000;
            }
        }

        static string Repo
        {
            get
            {
                // Policy only: HKLM\SOFTWARE\Policies is writable by administrators alone.
                try
                {
                    using (RegistryKey hklm = RegistryKey.OpenBaseKey(RegistryHive.LocalMachine, RegistryView.Registry64))
                    using (RegistryKey k = hklm.OpenSubKey(@"SOFTWARE\Policies\Foghorn"))
                    {
                        string r = k == null ? "" : Convert.ToString(k.GetValue("UpdateRepo", "")).Trim();
                        // A string, not a char[] literal: csc turns a constant array of
                        // several values into a class with a random GUID in its name,
                        // and dist\FoghornClient.exe would never match its source in CI.
                        bool clean = r.Length > 3 && r.IndexOf('/') > 0;
                        foreach (char ch in r) if (" ?#\\.".IndexOf(ch) >= 0) clean = false;
                        if (clean) return r;
                    }
                }
                catch { }
                return DefaultRepo;
            }
        }

        /// <summary>
        /// SYSTEM's own profile. Unlike ProgramData, students cannot create or
        /// change anything here, so a checked download cannot be swapped.
        /// </summary>
        static string WorkDir
        {
            get
            {
                string dir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), @"Foghorn\updater");
                Directory.CreateDirectory(dir);
                return dir;
            }
        }

        static void Write(string message)
        {
            try
            {
                string path = Path.Combine(WorkDir, "updater.log");
                var fi = new FileInfo(path);
                if (fi.Exists && fi.Length > 256 * 1024) { File.Delete(path + ".1"); File.Move(path, path + ".1"); }
                File.AppendAllText(path, DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss") + "  " + message + Environment.NewLine);
            }
            catch { }
        }

        /// <summary>One round: tell the server who we are; update if it says so. Returns seconds until the next check.</summary>
        static int CheckOnce(WaitHandle stopping)
        {
            Config cfg = Config.Load();
            if (!cfg.IsValid) return 1200;
            Dictionary<string, object> reply = Ask(cfg);
            int next = Num(reply, "check_seconds", 1200);
            var update = Get(reply, "update") as Dictionary<string, object>;
            if (update == null) { state = "idle"; detail = ""; return next; }

            string ver = Str(update, "version"), sha = Str(update, "sha256").ToLowerInvariant();
            if (!ValidVersion(ver) || !Newer(ver, Program.Version) || sha.Length != 64) return next;
            if (RecentlyFailed(ver)) return next;

            Write("Server asks for version " + ver + " (running " + Program.Version + ")");
            state = "downloading"; detail = "";
            TryAsk(cfg);
            string msi = Download(cfg, ver, sha, Get(update, "server_copy") is bool && (bool)Get(update, "server_copy"));
            if (msi == null) { state = "failed"; TryAsk(cfg); NoteFailure(ver); return next; }
            if (stopping.WaitOne(0)) return next;

            state = "installing"; detail = "";
            TryAsk(cfg);
            Install(cfg, msi, ver, sha);
            return next;
        }

        // ---- talking to the server ----

        static Dictionary<string, object> Ask(Config cfg)
        {
            var body = new Dictionary<string, object>();
            body["hostname"] = Identity.Hostname;
            body["domain"] = MachineDomain();
            body["ou"] = Identity.OU;
            body["version"] = Program.Version;
            body["state"] = state;
            body["detail"] = detail;
            byte[] payload = Encoding.UTF8.GetBytes(new JavaScriptSerializer().Serialize(body));
            var req = (HttpWebRequest)WebRequest.Create(cfg.ServerUrl.TrimEnd(new[] { '/' }) + "/api/client/updater");
            req.Method = "POST";
            req.ContentType = "application/json";
            req.UserAgent = "FoghornUpdater/" + Program.Version;
            req.Headers["X-Foghorn-Key"] = cfg.ClientKey;
            req.Timeout = 30000;
            req.AllowAutoRedirect = false;
            if (!cfg.UseSystemProxy) req.Proxy = null;
            using (Stream s = req.GetRequestStream()) s.Write(payload, 0, payload.Length);
            using (var resp = (HttpWebResponse)req.GetResponse())
            using (var reader = new StreamReader(resp.GetResponseStream(), Encoding.UTF8))
                return new JavaScriptSerializer().DeserializeObject(reader.ReadToEnd()) as Dictionary<string, object>
                       ?? new Dictionary<string, object>();
        }

        static void TryAsk(Config cfg) { try { Ask(cfg); } catch { } }

        // ---- downloading ----

        static string Download(Config cfg, string ver, string sha, bool serverHasCopy)
        {
            string path = Path.Combine(WorkDir, "FoghornClient-" + ver + ".msi");
            string gh = "https://github.com/" + Repo + "/releases/download/v" + ver + "/FoghornClient.msi";
            string problem = Fetch(gh, path, sha, null, true);
            if (problem == null) { Write("Downloaded " + ver + " from GitHub"); return path; }
            Write("GitHub: " + problem);

            Uri server;
            if (serverHasCopy && Uri.TryCreate(cfg.ServerUrl, UriKind.Absolute, out server) && server.Scheme == "https")
            {
                string url = cfg.ServerUrl.TrimEnd(new[] { '/' }) + "/api/client/updater/msi?version=" + ver;
                string p2 = Fetch(url, path, sha, cfg, false);
                if (p2 == null) { Write("Downloaded " + ver + " from the Foghorn server"); return path; }
                problem += "; Foghorn server: " + p2;
            }
            else if (serverHasCopy)
            {
                problem += ". (The Foghorn server has a copy, but it is not on HTTPS, so it is not used: over plain HTTP the file could be swapped on the way.)";
            }
            detail = "Could not download the update. " + problem;
            Write(detail);
            return null;
        }

        /// <summary>Downloads url to path and checks its SHA-256. Returns null on success, or what went wrong.</summary>
        static string Fetch(string url, string path, string sha, Config withKey, bool githubOnly)
        {
            string tmp = path + ".part";
            try
            {
                var req = (HttpWebRequest)WebRequest.Create(url);
                req.UserAgent = "FoghornUpdater/" + Program.Version;
                req.Timeout = 60000; req.ReadWriteTimeout = 60000;
                req.AllowAutoRedirect = true; req.MaximumAutomaticRedirections = 5;
                if (withKey != null) { req.Headers["X-Foghorn-Key"] = withKey.ClientKey; req.AllowAutoRedirect = false; if (!withKey.UseSystemProxy) req.Proxy = null; }
                using (var resp = (HttpWebResponse)req.GetResponse())
                {
                    // GitHub sends downloads on to its storage host. They must stay on HTTPS.
                    if (resp.ResponseUri.Scheme != "https") return "the download was redirected away from HTTPS";
                    if (githubOnly && !(resp.ResponseUri.Host.EndsWith(".githubusercontent.com") || resp.ResponseUri.Host == "github.com"))
                        return "the download was redirected to " + resp.ResponseUri.Host;
                    using (Stream src = resp.GetResponseStream())
                    using (var dst = File.Create(tmp))
                    {
                        var buf = new byte[65536];
                        long total = 0;
                        int n;
                        while ((n = src.Read(buf, 0, buf.Length)) > 0)
                        {
                            total += n;
                            if (total > 100L * 1024 * 1024) return "the download is far larger than an MSI should be";
                            dst.Write(buf, 0, n);
                        }
                    }
                }
                if (Sha256(tmp) != sha) { File.Delete(tmp); return "the file did not match the checksum from the server"; }
                if (File.Exists(path)) File.Delete(path);
                File.Move(tmp, path);
                return null;
            }
            catch (WebException ex)
            {
                var http = ex.Response as HttpWebResponse;
                return http != null ? "HTTP " + (int)http.StatusCode : ex.Message;
            }
            catch (Exception ex) { return ex.Message; }
            finally { try { if (File.Exists(tmp)) File.Delete(tmp); } catch { } }
        }

        static string Sha256(string path)
        {
            using (var sha = SHA256.Create())
            using (FileStream f = File.OpenRead(path))
                return BitConverter.ToString(sha.ComputeHash(f)).Replace("-", "").ToLowerInvariant();
        }

        // ---- installing ----

        static string PendingFile { get { return Path.Combine(WorkDir, "pending.txt"); } }

        static void Install(Config cfg, string msi, string ver, string sha)
        {
            // Hold the file open, read-only for everyone else, until msiexec has
            // used it, and check it once more under that lock.
            using (var hold = new FileStream(msi, FileMode.Open, FileAccess.Read, FileShare.Read))
            {
                using (var h = SHA256.Create())
                    if (BitConverter.ToString(h.ComputeHash(hold)).Replace("-", "").ToLowerInvariant() != sha)
                    { state = "failed"; detail = "The downloaded file changed before it could be installed."; NoteFailure(ver); return; }

                File.WriteAllText(PendingFile, ver + "\n" + Program.Version + "\n" + DateTime.UtcNow.ToString("o"));
                string log = Path.Combine(WorkDir, "install-" + ver + ".log");
                var args = new StringBuilder("/i \"" + msi + "\" /qn /norestart /l*v \"" + log + "\"");
                // A major upgrade removes the old settings with the old version.
                // Hand them over when they came from the installer; Group Policy
                // settings live elsewhere and are untouched.
                if (cfg.Source.StartsWith("the local registry"))
                {
                    args.Append(" SERVERURL=\"" + cfg.ServerUrl.Replace("\"", "") + "\" CLIENTKEY=\"" + cfg.ClientKey.Replace("\"", "") + "\"");
                    if (cfg.UseSystemProxy) args.Append(" USESYSTEMPROXY=1");
                }
                StopDesktopClients();
                Write("Installing " + ver + ": msiexec " + args.ToString().Replace(cfg.ClientKey, "<key>"));
                using (Process p = Process.Start(new ProcessStartInfo("msiexec.exe", args.ToString()) { UseShellExecute = false, CreateNoWindow = true }))
                {
                    // Normally Windows Installer stops this service part-way through
                    // and the new one finishes the job (FinishPendingInstall). If
                    // msiexec returns while we are still running, it failed early.
                    p.WaitForExit();
                    int code = p.ExitCode;
                    if (code == 0 || code == 3010) return; // the new service takes it from here
                    state = "failed";
                    detail = "msiexec failed with exit code " + code + " (log: " + log + ")";
                    Write(detail);
                    NoteFailure(ver);
                    File.Delete(PendingFile);
                    StartDesktopClients();
                }
            }
        }

        /// <summary>Runs when the service starts: finishes off an update that stopped the old service.</summary>
        static void FinishPendingInstall()
        {
            try
            {
                if (!File.Exists(PendingFile)) return;
                string[] lines = File.ReadAllLines(PendingFile);
                File.Delete(PendingFile);
                string wanted = lines.Length > 0 ? lines[0].Trim() : "";
                if (wanted == Program.Version) { Write("Updated to " + Program.Version); state = "idle"; detail = ""; }
                else { state = "failed"; detail = "The update to " + wanted + " did not complete; still on " + Program.Version + ". See install-" + wanted + ".log."; Write(detail); NoteFailure(wanted); }
                StartDesktopClients();
                TryAsk(Config.Load());
            }
            catch (Exception ex) { Write("After update: " + ex.Message); }
        }

        static string FailFile { get { return Path.Combine(WorkDir, "failed.txt"); } }

        static void NoteFailure(string ver) { try { File.WriteAllText(FailFile, ver + "\n" + DateTime.UtcNow.ToString("o")); } catch { } }

        /// <summary>Do not retry a version that failed in the last six hours.</summary>
        static bool RecentlyFailed(string ver)
        {
            try
            {
                if (!File.Exists(FailFile)) return false;
                string[] l = File.ReadAllLines(FailFile);
                DateTime when;
                return l.Length > 1 && l[0] == ver &&
                       DateTime.TryParse(l[1], CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind, out when) &&
                       DateTime.UtcNow - when.ToUniversalTime() < TimeSpan.FromHours(6);
            }
            catch { return false; }
        }

        // ---- the desktop clients ----

        static void StopDesktopClients()
        {
            int me = Process.GetCurrentProcess().Id;
            foreach (Process p in Process.GetProcessesByName("FoghornClient"))
            {
                try { if (p.Id != me) p.Kill(); } catch { }
                p.Dispose();
            }
        }

        /// <summary>Starts the desktop client again in every signed-in session.</summary>
        public static void StartDesktopClients()
        {
            string exe = Path.Combine(Path.GetDirectoryName(System.Windows.Forms.Application.ExecutablePath), "FoghornClient.exe");
            foreach (int session in Native.UserSessions())
            {
                string problem = Native.StartInSession(session, exe);
                if (problem != null) Write("Could not start the client in session " + session + ": " + problem);
            }
        }

        // ---- --update-now ----

        /// <summary>Asks the running updater service to check straight away. Needs an administrator.</summary>
        public static string AskServiceToCheck()
        {
            try
            {
                using (var sc = new ServiceController(UpdaterService.Name))
                {
                    if (sc.Status != ServiceControllerStatus.Running) return "The Foghorn Updater service is not running (" + sc.Status + ").";
                    sc.ExecuteCommand(UpdaterService.CheckNowCommand);
                }
                return null;
            }
            catch (InvalidOperationException ex)
            {
                return ex.InnerException is System.ComponentModel.Win32Exception && ((System.ComponentModel.Win32Exception)ex.InnerException).NativeErrorCode == 5
                    ? "Run this as an administrator."
                    : "The Foghorn Updater service is not installed. It comes with the 1.2 MSI.";
            }
        }

        /// <summary>For --status: whether the updater service is here and running.</summary>
        public static string ServiceStatus()
        {
            try { using (var sc = new ServiceController(UpdaterService.Name)) return sc.Status.ToString(); }
            catch { return "not installed (comes with the MSI)"; }
        }

        // ---- small helpers ----

        public static bool ValidVersion(string v)
        {
            string[] parts = v.Split('.');
            if (parts.Length < 2 || parts.Length > 4) return false;
            foreach (string p in parts) { int n; if (p == "" || p.Length > 5 || !int.TryParse(p, out n)) return false; }
            return true;
        }

        /// <summary>Is a newer than b? Dotted numbers: 1.10.0 is newer than 1.9.2.</summary>
        public static bool Newer(string a, string b)
        {
            string[] x = a.Split('.'), y = b.Split('.');
            for (int i = 0; i < Math.Max(x.Length, y.Length); i++)
            {
                int p = 0, q = 0;
                if (i < x.Length) int.TryParse(x[i], out p);
                if (i < y.Length) int.TryParse(y[i], out q);
                if (p != q) return p > q;
            }
            return false;
        }

        static string MachineDomain()
        {
            try { return System.Net.NetworkInformation.IPGlobalProperties.GetIPGlobalProperties().DomainName; } catch { return ""; }
        }

        static object Get(Dictionary<string, object> d, string k) { object v; return d != null && d.TryGetValue(k, out v) ? v : null; }
        static string Str(Dictionary<string, object> d, string k) { object v = Get(d, k); return v == null ? "" : v.ToString(); }
        static int Num(Dictionary<string, object> d, string k, int fallback)
        {
            try { object v = Get(d, k); return v == null ? fallback : Convert.ToInt32(v, CultureInfo.InvariantCulture); } catch { return fallback; }
        }
    }
}
