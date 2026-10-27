// Foghorn desktop client.
//
// Runs quietly in each logged-on user's session, keeps one HTTP request open
// to the Foghorn server, and shows whatever alerts arrive on top of every
// other window.
//
// Written in C# 5 against .NET Framework 4.x on purpose: that runtime is part
// of Windows 10 and 11, and the compiler for it (csc.exe) ships inside Windows,
// so build.cmd can rebuild this on any PC with nothing extra installed.

using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Reflection;
using System.Security.Principal;
using System.Text;
using System.Threading;
using System.Windows.Forms;
using Microsoft.Win32;

// These are what Windows shows on the Details tab of the exe's properties, and
// what an administrator sees when they are working out where a program on their
// estate came from. Keep the versions in step with Program.Version below.
[assembly: AssemblyTitle("Foghorn desktop alerts")]
[assembly: AssemblyProduct("Foghorn")]
[assembly: AssemblyDescription("Shows alerts sent from the Foghorn server")]
[assembly: AssemblyCompany("Archie Paice")]
[assembly: AssemblyCopyright("Copyright (c) 2026 Archie Paice. MIT licence.")]
[assembly: AssemblyVersion("1.1.0.0")]
[assembly: AssemblyFileVersion("1.1.0.0")]

namespace Foghorn
{
    static class Program
    {
        public const string Version = "1.1.0";

        /// <summary>
        /// Shown in --help and --status. Whoever is troubleshooting a PC should
        /// be able to see who wrote this and how to reach them without having
        /// to go and find the repository.
        /// </summary>
        public const string Credit = "Foghorn was designed and built by Archie Paice.\n" +
                                     "Contact:  hello@archiepaice.com";

        [STAThread]
        static int Main(string[] args)
        {
            Native.MakeDpiAware();
            Application.EnableVisualStyles();
            Application.SetCompatibleTextRenderingDefault(false);

            var opts = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
            for (int i = 0; i < args.Length; i++)
            {
                string a = args[i].TrimStart('-', '/').ToLowerInvariant();
                if ((a == "server" || a == "key") && i + 1 < args.Length) opts[a] = args[++i];
                else opts[a] = "1";
            }
            Config.Overrides = opts;

            if (opts.ContainsKey("test")) { Application.Run(new TestContext()); return 0; }
            if (opts.ContainsKey("test-lock")) { Application.Run(new TestLockContext()); return 0; }
            if (opts.ContainsKey("status")) { StatusCheck.Run(); return 0; }
            if (opts.ContainsKey("help") || opts.ContainsKey("?"))
            {
                MessageBox.Show(
                    "FoghornClient.exe              run normally (no window; alerts pop up when sent)\n" +
                    "FoghornClient.exe --test       show sample alerts without needing a server\n" +
                    "FoghornClient.exe --test-lock  lock this screen for 10 seconds, to see what a lock looks like\n" +
                    "FoghornClient.exe --status     show the settings in use and test the connection\n" +
                    "  --server URL --key KEY     override the configured server for this run\n\n" +
                    Credit,
                    "Foghorn client " + Version, MessageBoxButtons.OK, MessageBoxIcon.Information);
                return 0;
            }

            // One client per Windows session. "Local\" scopes the mutex to the
            // session, so two people on one PC (fast user switching, RDS) each
            // get their own.
            bool first;
            using (var mutex = new Mutex(true, @"Local\FoghornClient", out first))
            {
                if (!first) return 0;
                Log.Write("Foghorn client " + Version + " started for " + Identity.Domain + "\\" + Identity.User + " on " + Identity.Hostname);
                Application.ThreadException += delegate(object s, ThreadExceptionEventArgs e) { Log.Write("UI error: " + e.Exception); };
                AppDomain.CurrentDomain.UnhandledException += delegate(object s, UnhandledExceptionEventArgs e) { Log.Write("Fatal: " + e.ExceptionObject); };
                Application.Run(new ClientContext());
                GC.KeepAlive(mutex);
            }
            return 0;
        }
    }

    /// <summary>Normal running mode: a message loop with no visible window.</summary>
    class ClientContext : ApplicationContext
    {
        readonly Control marshal = new Control();
        readonly AlertManager manager;
        readonly LockManager locks;
        readonly Poller poller;

        public ClientContext()
        {
            IntPtr force = marshal.Handle; // create the handle on the UI thread so BeginInvoke works
            GC.KeepAlive(force);
            poller = new Poller();
            manager = new AlertManager(poller.QueueEvent);
            locks = new LockManager(poller.QueueLockEvent, manager);
            poller.LocksReceived += delegate(List<LockData> apply, List<string> lift)
            {
                try
                {
                    marshal.BeginInvoke((MethodInvoker)delegate
                    {
                        foreach (string id in lift) locks.Release(id);
                        foreach (LockData l in apply) locks.Apply(l);
                    });
                }
                catch (Exception ex) { Log.Write("Could not hand locks to the UI: " + ex.Message); }
            };
            poller.AlertsReceived += delegate(List<AlertData> alerts, List<string> recalled)
            {
                try
                {
                    marshal.BeginInvoke((MethodInvoker)delegate
                    {
                        foreach (string id in recalled) manager.Recall(id);
                        foreach (AlertData a in alerts) manager.Show(a);
                    });
                }
                catch (Exception ex) { Log.Write("Could not hand alerts to the UI: " + ex.Message); }
            };
            poller.Start();
        }
    }

    /// <summary>--test: shows one of each style so an admin can see it working.</summary>
    class TestContext : ApplicationContext
    {
        public TestContext()
        {
            int open = 0;
            AlertManager manager = null;
            manager = new AlertManager(delegate(string id, string ev, string choice)
            {
                if (ev == "displayed") return;
                open--;
                if (id == "test-3") ExitThread();
                else if (open == 0)
                {
                    var full = Sample("test-3", "Leave the building now", "This is what a full-screen alert looks like. It covers every monitor until it is acknowledged.\n\nThis is only a test.", "critical", "fullscreen");
                    full.RequireAck = true;
                    full.Icon = "\U0001F6A8";
                    full.Responses.Add("I’m out safely");
                    full.Responses.Add("I need help");
                    open++;
                    manager.Show(full);
                }
            });
            var a = Sample("test-1", "Save your work", "This is a corner alert. It sits on top of everything but does not take the keyboard away from what you were typing.", "info", "corner");
            a.Link = "https://www.example.com/";
            a.Icon = "\U0001F4C5";
            var b = Sample("test-2", "This computer will restart soon", "This is a centre alert with its own colour and label. Close both of these to see the full-screen style.", "warning", "center");
            b.Accent = "#6a3fb5"; b.Label = "IT team"; b.Icon = "\U0001F527"; b.DismissLabel = "Got it";
            open = 2;
            manager.Show(a);
            manager.Show(b);
        }

        static AlertData Sample(string id, string title, string msg, string level, string display)
        {
            var a = new AlertData();
            a.Id = id; a.Title = title; a.Message = msg; a.Level = level; a.Display = display;
            a.Sender = "Foghorn test"; a.Org = "Foghorn"; a.Created = DateTime.Now; a.Sound = true;
            return a;
        }
    }

    /// <summary>--test-lock: locks this screen for ten seconds so an admin can see a lock working.</summary>
    class TestLockContext : ApplicationContext
    {
        public TestLockContext()
        {
            var manager = new AlertManager(delegate(string id, string ev, string choice) { });
            LockManager locks = null;
            locks = new LockManager(delegate(string id, string ev) { if (ev == "released") ExitThread(); }, manager);
            var l = new LockData();
            l.Id = "test-lock"; l.Mode = "blank"; l.Sender = "Foghorn test"; l.Org = "Foghorn";
            l.Message = "This is a test lock. Keyboard and mouse are blocked. It lifts itself in 10 seconds.";
            l.EndsUtc = DateTime.UtcNow.AddSeconds(10);
            locks.Apply(l);
        }
    }

    /// <summary>--status: a plain-English self-check for whoever is troubleshooting.</summary>
    static class StatusCheck
    {
        public static void Run()
        {
            var sb = new StringBuilder();
            Config cfg = Config.Load();
            sb.AppendLine("Foghorn client " + Program.Version);
            sb.AppendLine();
            sb.AppendLine("Computer:  " + Identity.Hostname);
            sb.AppendLine("User:  " + Identity.Domain + "\\" + Identity.User);
            sb.AppendLine("OU:  " + (Identity.OU == "" ? "(not domain joined, or no Group Policy applied yet)" : Identity.OU));
            sb.AppendLine("AD groups reported:  " + Identity.Groups.Count);
            sb.AppendLine();
            sb.AppendLine("Server address:  " + (cfg.ServerUrl == "" ? "NOT SET" : cfg.ServerUrl));
            sb.AppendLine("Client key:  " + (cfg.ClientKey == "" ? "NOT SET" : "set (" + cfg.ClientKey.Length + " characters)"));
            sb.AppendLine("Settings came from:  " + cfg.Source);
            sb.AppendLine();
            MessageBoxIcon icon = MessageBoxIcon.Warning;
            if (!cfg.IsValid)
            {
                sb.AppendLine("The client has nothing to connect to. Set ServerUrl and ClientKey with the Foghorn Group Policy template, or re-run Install-FoghornClient.ps1 with -ServerUrl and -ClientKey.");
            }
            else
            {
                string problem = Poller.TestConnection(cfg);
                if (problem == null) { sb.AppendLine("Connection test:  OK - this PC can reach the server and the key was accepted."); icon = MessageBoxIcon.Information; }
                else sb.AppendLine("Connection test:  FAILED\n" + problem);
            }
            sb.AppendLine();
            sb.AppendLine("Log file:  " + Log.FilePath);
            sb.AppendLine();
            sb.AppendLine(Program.Credit);
            MessageBox.Show(sb.ToString(), "Foghorn client status", MessageBoxButtons.OK, icon);
        }
    }

    class Config
    {
        public static Dictionary<string, string> Overrides = new Dictionary<string, string>();

        public string ServerUrl = "";
        public string ClientKey = "";
        public bool UseSystemProxy;
        public string Source = "nowhere - no settings found";

        public bool IsValid
        {
            get
            {
                Uri u;
                return ClientKey != "" && Uri.TryCreate(ServerUrl, UriKind.Absolute, out u) && (u.Scheme == "http" || u.Scheme == "https");
            }
        }

        /// <summary>
        /// Settings are looked for in this order; the first place that has a
        /// ServerUrl wins:
        ///   1. command line (--server, --key)
        ///   2. HKLM\SOFTWARE\Policies\Foghorn   (Group Policy - Foghorn.admx)
        ///   3. HKLM\SOFTWARE\Foghorn            (written by Install-FoghornClient.ps1 / the MSI)
        ///   4. foghorn-client.ini next to the exe
        /// Both the 64-bit and 32-bit registry views are checked.
        /// </summary>
        public static Config Load()
        {
            var c = new Config();
            string v;
            if (Overrides.TryGetValue("server", out v))
            {
                c.ServerUrl = v.Trim();
                if (Overrides.TryGetValue("key", out v)) c.ClientKey = v.Trim();
                c.Source = "the command line";
                FillMissingKey(c);
                return c;
            }
            string[] keys = { @"SOFTWARE\Policies\Foghorn", @"SOFTWARE\Foghorn" };
            string[] names = { "Group Policy", "the local registry (installer)" };
            for (int i = 0; i < keys.Length; i++)
            {
                foreach (RegistryView view in new[] { RegistryView.Registry64, RegistryView.Registry32 })
                {
                    if (ReadRegistry(c, keys[i], view)) { c.Source = names[i] + "  (HKLM\\" + keys[i] + ")"; return c; }
                }
            }
            try
            {
                string ini = Path.Combine(Path.GetDirectoryName(Application.ExecutablePath), "foghorn-client.ini");
                if (File.Exists(ini))
                {
                    foreach (string raw in File.ReadAllLines(ini))
                    {
                        string line = raw.Trim();
                        int eq = line.IndexOf('=');
                        if (line.StartsWith("#") || line.StartsWith(";") || eq < 1) continue;
                        string k = line.Substring(0, eq).Trim().ToLowerInvariant(), val = line.Substring(eq + 1).Trim();
                        if (k == "serverurl") c.ServerUrl = val;
                        else if (k == "clientkey") c.ClientKey = val;
                        else if (k == "usesystemproxy") c.UseSystemProxy = val == "1" || val.ToLowerInvariant() == "true";
                    }
                    if (c.ServerUrl != "") c.Source = ini;
                }
            }
            catch (Exception ex) { Log.Write("Could not read foghorn-client.ini: " + ex.Message); }
            return c;
        }

        static void FillMissingKey(Config c)
        {
            if (c.ClientKey != "") return;
            var saved = Overrides; Overrides = new Dictionary<string, string>();
            try { c.ClientKey = Load().ClientKey; } finally { Overrides = saved; }
        }

        static bool ReadRegistry(Config c, string path, RegistryView view)
        {
            try
            {
                using (RegistryKey hklm = RegistryKey.OpenBaseKey(RegistryHive.LocalMachine, view))
                using (RegistryKey k = hklm.OpenSubKey(path))
                {
                    if (k == null) return false;
                    string url = Convert.ToString(k.GetValue("ServerUrl", "")).Trim();
                    if (url == "") return false;
                    c.ServerUrl = url;
                    c.ClientKey = Convert.ToString(k.GetValue("ClientKey", "")).Trim();
                    c.UseSystemProxy = Convert.ToString(k.GetValue("UseSystemProxy", "0")) == "1";
                    return true;
                }
            }
            catch { return false; }
        }
    }

    /// <summary>Who and where this client is. Worked out once, lazily.</summary>
    static class Identity
    {
        public static readonly string Hostname = Environment.MachineName;
        public static readonly string User = Environment.UserName;
        public static readonly string Domain = Environment.UserDomainName;
        public static readonly string SessionId = Guid.NewGuid().ToString("N");

        static string ou, os;
        static List<string> groups;

        /// <summary>The computer's distinguished name, which Group Policy caches in the registry.</summary>
        public static string OU
        {
            get
            {
                if (ou != null) return ou;
                ou = "";
                try
                {
                    using (RegistryKey hklm = RegistryKey.OpenBaseKey(RegistryHive.LocalMachine, RegistryView.Registry64))
                    using (RegistryKey k = hklm.OpenSubKey(@"SOFTWARE\Microsoft\Windows\CurrentVersion\Group Policy\State\Machine"))
                        if (k != null) ou = Convert.ToString(k.GetValue("Distinguished-Name", ""));
                }
                catch { }
                return ou;
            }
        }

        /// <summary>Domain groups in the user's logon token (so nested groups are included).</summary>
        public static List<string> Groups
        {
            get
            {
                if (groups != null) return groups;
                var list = new List<string>();
                try
                {
                    using (WindowsIdentity me = WindowsIdentity.GetCurrent())
                    {
                        foreach (IdentityReference sid in me.Groups)
                        {
                            try
                            {
                                string name = sid.Translate(typeof(NTAccount)).Value;
                                int slash = name.IndexOf('\\');
                                if (slash < 1) continue; // Everyone, LOCAL, ...
                                string authority = name.Substring(0, slash).ToUpperInvariant();
                                if (authority == "NT AUTHORITY" || authority == "BUILTIN" || authority == "MANDATORY LABEL" ||
                                    authority == "NT SERVICE" || authority == Hostname.ToUpperInvariant()) continue;
                                list.Add(name);
                                if (list.Count >= 300) break;
                            }
                            catch { /* a SID that will not translate is not worth failing over */ }
                        }
                    }
                }
                catch (Exception ex) { Log.Write("Could not read group membership: " + ex.Message); }
                list.Sort(StringComparer.OrdinalIgnoreCase);
                groups = list;
                return groups;
            }
        }

        public static string OS
        {
            get
            {
                if (os != null) return os;
                os = Environment.OSVersion.VersionString;
                try
                {
                    using (RegistryKey hklm = RegistryKey.OpenBaseKey(RegistryHive.LocalMachine, RegistryView.Registry64))
                    using (RegistryKey k = hklm.OpenSubKey(@"SOFTWARE\Microsoft\Windows NT\CurrentVersion"))
                    {
                        if (k != null)
                        {
                            string product = Convert.ToString(k.GetValue("ProductName", "Windows"));
                            string build = Convert.ToString(k.GetValue("CurrentBuild", ""));
                            string release = Convert.ToString(k.GetValue("DisplayVersion", ""));
                            int n;
                            // Windows 11 still calls itself "Windows 10" in this value.
                            if (int.TryParse(build, out n) && n >= 22000) product = product.Replace("Windows 10", "Windows 11");
                            os = (product + " " + release).Trim() + " (build " + build + ")";
                        }
                    }
                }
                catch { }
                return os;
            }
        }
    }

    static class Log
    {
        static readonly object gate = new object();
        public static readonly string FilePath = MakePath();

        static string MakePath()
        {
            try
            {
                string dir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Foghorn");
                Directory.CreateDirectory(dir);
                return Path.Combine(dir, "client.log");
            }
            catch { return Path.Combine(Path.GetTempPath(), "foghorn-client.log"); }
        }

        public static void Write(string message)
        {
            try
            {
                lock (gate)
                {
                    var fi = new FileInfo(FilePath);
                    if (fi.Exists && fi.Length > 512 * 1024)
                    {
                        string old = FilePath + ".1";
                        if (File.Exists(old)) File.Delete(old);
                        File.Move(FilePath, old);
                    }
                    File.AppendAllText(FilePath, DateTime.Now.ToString("yyyy-MM-dd HH:mm:ss") + "  " + message + Environment.NewLine);
                }
                if (Config.Overrides.ContainsKey("debug")) Debug.WriteLine(message);
            }
            catch { /* logging must never take the client down */ }
        }
    }
}
