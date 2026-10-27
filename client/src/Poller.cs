using System;
using System.Collections;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Net;
using System.Text;
using System.Threading;
using System.Web.Script.Serialization;

namespace Foghorn
{
    /// <summary>One alert as the server describes it.</summary>
    class AlertData
    {
        public string Id = "", Title = "", Message = "", Level = "info", Display = "corner", Link = "", Sender = "", Org = "";
        public bool RequireAck, Sound;
        public int DisplaySeconds;
        public DateTime Created = DateTime.Now;

        // From the alert builder. Empty means "as the level".
        public string Accent = "", Label = "", Icon = "", DismissLabel = "", SoundId = "", SoundExt = "";
        public bool SoundLoop;
        public List<string> Responses = new List<string>();
    }

    /// <summary>One screen lock as the server describes it.</summary>
    class LockData
    {
        public string Id = "", Mode = "blank", Message = "", Sender = "", Org = "";
        /// <summary>When it lets go by itself, worked out from seconds_left so a wrong PC clock does not matter.</summary>
        public DateTime EndsUtc;
    }

    /// <summary>Something that happened on this PC, waiting to be told to the server.</summary>
    class OutEvent
    {
        public string AlertId = "", LockId = "", Event = "", Choice = "";
    }

    /// <summary>
    /// Talks to the server on a background thread.
    ///
    /// It keeps one "long poll" request open: the server holds it for about 25
    /// seconds and answers the instant an alert is sent. Anything that happened
    /// on this PC (alert shown, acknowledged, closed) rides along on the next
    /// request; if something happens while a request is waiting, that request is
    /// abandoned and a fresh one carries the news straight away.
    /// </summary>
    class Poller
    {
        public delegate void AlertsHandler(List<AlertData> alerts, List<string> recalled);
        public event AlertsHandler AlertsReceived;
        public delegate void LocksHandler(List<LockData> locks, List<string> unlocks);
        public event LocksHandler LocksReceived;

        readonly object gate = new object();
        readonly List<OutEvent> pending = new List<OutEvent>();
        readonly Random random = new Random();
        HttpWebRequest current;
        bool abortedByUs;
        string lastState = "";

        static Poller()
        {
            try
            {
                ServicePointManager.Expect100Continue = false;
                ServicePointManager.DefaultConnectionLimit = 4;
                // Older .NET 4.x defaults stop at TLS 1.0. Add 1.2 and, where the OS has it, 1.3.
                ServicePointManager.SecurityProtocol |= (SecurityProtocolType)3072;
                try { ServicePointManager.SecurityProtocol |= (SecurityProtocolType)12288; } catch { }
            }
            catch { }
        }

        public void Start()
        {
            var t = new Thread(Loop);
            t.IsBackground = true;
            t.Name = "Foghorn poller";
            t.Start();
        }

        /// <summary>Called from the UI thread when something happens to an alert. choice is the reply button pressed, if any.</summary>
        public void QueueEvent(string alertId, string what, string choice)
        {
            var e = new OutEvent();
            e.AlertId = alertId; e.Event = what; e.Choice = choice ?? "";
            Queue(e);
        }

        /// <summary>Called from the UI thread when a lock is applied or let go.</summary>
        public void QueueLockEvent(string lockId, string what)
        {
            var e = new OutEvent();
            e.LockId = lockId; e.Event = what;
            Queue(e);
        }

        void Queue(OutEvent e)
        {
            lock (gate)
            {
                pending.Add(e);
                if (current != null)
                {
                    abortedByUs = true;
                    try { current.Abort(); } catch { }
                }
            }
        }

        void Loop()
        {
            int failures = 0;
            // Spread clients out a little so a whole room logging on at 9:00 does not hit the server in the same instant.
            Thread.Sleep(random.Next(200, 2500));
            while (true)
            {
                Config cfg = Config.Load();
                if (!cfg.IsValid)
                {
                    Report("Waiting for settings: no server address and client key found yet (Group Policy may not have applied). Checking again in a minute.");
                    Thread.Sleep(60000);
                    continue;
                }

                List<OutEvent> sending;
                lock (gate) { sending = new List<OutEvent>(pending); pending.Clear(); abortedByUs = false; }

                int waitMs;
                try
                {
                    Dictionary<string, object> reply = Send(cfg, sending, true, 45000, true);
                    failures = 0;
                    Report("Connected to " + cfg.ServerUrl);
                    Deliver(reply);
                    waitMs = Math.Max(250, Math.Min(10000, ToInt(Get(reply, "retry_ms"), 1000)));
                }
                catch (Exception ex)
                {
                    bool ours;
                    lock (gate) { ours = abortedByUs; pending.InsertRange(0, sending); } // nothing is lost: events are safe to send twice
                    if (ours) continue;

                    failures++;
                    var web = ex as WebException;
                    var http = web == null ? null : web.Response as HttpWebResponse;
                    if (http != null && (int)http.StatusCode == 401)
                    {
                        Report("The server rejected this PC's client key. Check the ClientKey setting matches Settings > Client key in the web console.");
                        waitMs = 60000;
                    }
                    else
                    {
                        Report("Cannot reach " + cfg.ServerUrl + " (" + ex.Message + "). Retrying.");
                        waitMs = (int)Math.Min(60, Math.Pow(2, Math.Min(failures, 6))) * 1000;
                    }
                    waitMs += random.Next(0, 3000);
                    if (http != null) try { http.Close(); } catch { }
                }

                // Sleep, but wake early if there is something to report.
                for (int slept = 0; slept < waitMs; slept += 100)
                {
                    lock (gate) { if (pending.Count > 0 && failures == 0) break; }
                    Thread.Sleep(100);
                }
            }
        }

        /// <summary>Log connection state only when it changes, so the log stays readable.</summary>
        void Report(string state)
        {
            if (state == lastState) return;
            lastState = state;
            Log.Write(state);
        }

        Dictionary<string, object> Send(Config cfg, List<OutEvent> events, bool wait, int timeoutMs, bool track)
        {
            var client = new Dictionary<string, object>();
            client["hostname"] = Identity.Hostname;
            client["user"] = Identity.User;
            client["domain"] = Identity.Domain;
            client["ou"] = Identity.OU;
            client["groups"] = Identity.Groups;
            client["version"] = Program.Version;
            client["os"] = Identity.OS;
            client["sid"] = Identity.SessionId;

            var evs = new List<object>();
            foreach (OutEvent e in events)
            {
                var d = new Dictionary<string, object>();
                if (e.LockId != "") d["lock_id"] = e.LockId; else d["alert_id"] = e.AlertId;
                d["event"] = e.Event;
                if (e.Choice != "") d["choice"] = e.Choice;
                evs.Add(d);
            }
            var body = new Dictionary<string, object>();
            body["client"] = client; body["events"] = evs; body["wait"] = wait;
            byte[] payload = Encoding.UTF8.GetBytes(new JavaScriptSerializer().Serialize(body));

            // NB: TrimEnd(new[] { '/' }), not TrimEnd('/'). The single-char overload
            // does not exist on .NET Framework 4.0; using it makes the client fail
            // with "Method not found" on PCs that only have the older runtime.
            var req = (HttpWebRequest)WebRequest.Create(cfg.ServerUrl.TrimEnd(new[] { '/' }) + "/api/client/poll");
            req.Method = "POST";
            req.ContentType = "application/json";
            req.Accept = "application/json";
            req.UserAgent = "FoghornClient/" + Program.Version;
            req.Headers["X-Foghorn-Key"] = cfg.ClientKey;
            req.Timeout = timeoutMs;
            req.ReadWriteTimeout = timeoutMs;
            req.KeepAlive = true;
            req.AllowAutoRedirect = false;
            // The server is on your own network, so by default skip proxy discovery
            // (WPAD can add a long delay and school filters may mangle the request).
            if (!cfg.UseSystemProxy) req.Proxy = null;

            if (track)
            {
                lock (gate)
                {
                    // Something happened between building this request and now: do not
                    // sit in a 25-second wait with news still in the queue.
                    if (pending.Count > 0 && wait) { abortedByUs = true; throw new OperationCanceledException(); }
                    current = req;
                }
            }
            try
            {
                using (Stream s = req.GetRequestStream()) s.Write(payload, 0, payload.Length);
                using (var resp = (HttpWebResponse)req.GetResponse())
                using (var reader = new StreamReader(resp.GetResponseStream(), Encoding.UTF8))
                {
                    string text = reader.ReadToEnd();
                    var ser = new JavaScriptSerializer();
                    ser.MaxJsonLength = 4 * 1024 * 1024;
                    var parsed = ser.DeserializeObject(text) as Dictionary<string, object>;
                    if (parsed == null) throw new InvalidDataException("The server's reply was not what a Foghorn server sends. Is ServerUrl pointing at the right place?");
                    return parsed;
                }
            }
            finally
            {
                if (track) lock (gate) { current = null; }
            }
        }

        void Deliver(Dictionary<string, object> reply)
        {
            var alerts = new List<AlertData>();
            foreach (object o in AsList(Get(reply, "alerts")))
            {
                var d = o as Dictionary<string, object>;
                if (d == null) continue;
                var a = new AlertData();
                a.Id = Str(d, "id"); a.Title = Str(d, "title"); a.Message = Str(d, "message");
                a.Level = Str(d, "level"); a.Display = Str(d, "display"); a.Link = Str(d, "link");
                a.Sender = Str(d, "sender"); a.Org = Str(d, "org");
                a.RequireAck = Get(d, "require_ack") is bool && (bool)Get(d, "require_ack");
                a.Sound = Get(d, "sound") is bool && (bool)Get(d, "sound");
                a.DisplaySeconds = ToInt(Get(d, "display_seconds"), 0);
                a.Accent = Str(d, "accent"); a.Label = Str(d, "label"); a.Icon = Str(d, "icon");
                a.DismissLabel = Str(d, "dismiss_label");
                a.SoundId = Str(d, "sound_id"); a.SoundExt = Str(d, "sound_ext");
                a.SoundLoop = Get(d, "sound_loop") is bool && (bool)Get(d, "sound_loop");
                foreach (object r in AsList(Get(d, "responses"))) if (r != null && r.ToString() != "") a.Responses.Add(r.ToString());
                DateTime when;
                if (DateTime.TryParse(Str(d, "created_at"), CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind, out when))
                    a.Created = when.ToLocalTime();
                if (a.Id != "") alerts.Add(a);
            }
            var recalled = new List<string>();
            foreach (object o in AsList(Get(reply, "recalled"))) if (o != null) recalled.Add(o.ToString());

            var locks = new List<LockData>();
            foreach (object o in AsList(Get(reply, "locks")))
            {
                var d = o as Dictionary<string, object>;
                if (d == null) continue;
                var l = new LockData();
                l.Id = Str(d, "id"); l.Mode = Str(d, "mode"); l.Message = Str(d, "message");
                l.Sender = Str(d, "sender"); l.Org = Str(d, "org");
                // A lock with no sensible end gets a short one rather than none.
                int left = ToInt(Get(d, "seconds_left"), 0);
                if (left <= 0 || left > 8 * 3600) left = 60;
                l.EndsUtc = DateTime.UtcNow.AddSeconds(left);
                if (l.Id != "") locks.Add(l);
            }
            var unlocks = new List<string>();
            foreach (object o in AsList(Get(reply, "unlocks"))) if (o != null) unlocks.Add(o.ToString());

            if (alerts.Count > 0 || recalled.Count > 0)
            {
                Log.Write("Received " + alerts.Count + " alert(s), " + recalled.Count + " recall(s)");
                AlertsHandler handler = AlertsReceived;
                if (handler != null) handler(alerts, recalled);
            }
            if (locks.Count > 0 || unlocks.Count > 0)
            {
                Log.Write("Received " + locks.Count + " lock(s), " + unlocks.Count + " unlock(s)");
                LocksHandler lh = LocksReceived;
                if (lh != null) lh(locks, unlocks);
            }
        }

        /// <summary>
        /// Fetches an uploaded sound into the local cache, or returns the cached
        /// copy. Sound ids are content hashes, so a cached file never goes stale.
        /// Returns "" if it cannot be had; the caller falls back to a Windows sound.
        /// </summary>
        public static string FetchSound(string id, string ext)
        {
            if (id == "" || (ext != ".wav" && ext != ".mp3")) return "";
            foreach (char ch in id) if (!Uri.IsHexDigit(ch)) return ""; // it becomes a file name
            try
            {
                string dir = Path.Combine(Path.GetDirectoryName(Log.FilePath), "sounds");
                Directory.CreateDirectory(dir);
                string path = Path.Combine(dir, id + ext);
                if (File.Exists(path)) return path;

                Config cfg = Config.Load();
                if (!cfg.IsValid) return "";
                var req = (HttpWebRequest)WebRequest.Create(cfg.ServerUrl.TrimEnd(new[] { '/' }) + "/api/client/sounds/" + id);
                req.Headers["X-Foghorn-Key"] = cfg.ClientKey;
                req.UserAgent = "FoghornClient/" + Program.Version;
                req.Timeout = 20000; req.ReadWriteTimeout = 20000;
                req.AllowAutoRedirect = false;
                if (!cfg.UseSystemProxy) req.Proxy = null;
                string tmp = path + "." + Guid.NewGuid().ToString("N") + ".tmp";
                using (var resp = (HttpWebResponse)req.GetResponse())
                using (Stream src = resp.GetResponseStream())
                using (var dst = File.Create(tmp))
                {
                    var buf = new byte[65536];
                    long total = 0;
                    int n;
                    while ((n = src.Read(buf, 0, buf.Length)) > 0)
                    {
                        total += n;
                        if (total > 6 * 1024 * 1024) throw new InvalidDataException("sound is larger than the server allows");
                        dst.Write(buf, 0, n);
                    }
                }
                try { File.Move(tmp, path); }
                catch { if (File.Exists(tmp)) File.Delete(tmp); } // another session got there first
                return File.Exists(path) ? path : "";
            }
            catch (Exception ex)
            {
                Log.Write("Could not fetch sound " + id + ": " + ex.Message);
                return "";
            }
        }

        /// <summary>Used by --status. Returns null when all is well, otherwise a sentence explaining what is wrong.</summary>
        public static string TestConnection(Config cfg)
        {
            try
            {
                new Poller().Send(cfg, new List<OutEvent>(), false, 10000, false);
                return null;
            }
            catch (WebException ex)
            {
                var http = ex.Response as HttpWebResponse;
                if (http != null && (int)http.StatusCode == 401) return "The server answered, but rejected the client key. Compare it with Settings > Client key in the web console.";
                if (http != null) return "The server answered with HTTP " + (int)http.StatusCode + ". Is ServerUrl the address of the Foghorn server (including the port)?";
                if (ex.Status == WebExceptionStatus.TrustFailure) return "This PC does not trust the server's HTTPS certificate. Deploy the issuing CA certificate to Trusted Root Certification Authorities (see docs/SECURITY.md).";
                if (ex.Status == WebExceptionStatus.NameResolutionFailure) return "The server name could not be found in DNS. Check the spelling of ServerUrl.";
                return "No answer from the server: " + ex.Message + "\nCheck the server is running, the port is right, and the firewall on the server allows it.";
            }
            catch (Exception ex) { return ex.Message; }
        }

        // ---- small JSON helpers (JavaScriptSerializer hands back dictionaries, ArrayLists or object[]) ----

        static object Get(Dictionary<string, object> d, string key)
        {
            object v;
            return d != null && d.TryGetValue(key, out v) ? v : null;
        }

        static string Str(Dictionary<string, object> d, string key)
        {
            object v = Get(d, key);
            return v == null ? "" : v.ToString();
        }

        static int ToInt(object v, int fallback)
        {
            try { return v == null ? fallback : Convert.ToInt32(v, CultureInfo.InvariantCulture); } catch { return fallback; }
        }

        static IEnumerable AsList(object v)
        {
            var e = v as IEnumerable;
            if (e == null || v is string) return new object[0];
            return e;
        }
    }
}
