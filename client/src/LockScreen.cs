using System;
using System.Collections.Generic;
using System.Drawing;
using System.Runtime.InteropServices;
using System.Windows.Forms;

namespace Foghorn
{
    /// <summary>
    /// Locks the screen and blocks the keyboard and mouse while a teacher has
    /// this PC locked. Runs on the UI thread only.
    ///
    ///   blank  - every monitor is covered with the teacher's message.
    ///   freeze - the screen stays visible (so the class can still see a
    ///            presentation, say) behind a transparent cover, with a banner.
    ///
    /// In both modes a low-level keyboard hook swallows every key and a
    /// low-level mouse hook freezes the pointer. Windows does not let any
    /// program block Ctrl+Alt+Del, so the person at the PC can always sign out:
    /// that is deliberate, and the lock comes straight back if they sign in
    /// again while it is still in force.
    ///
    /// Alerts still appear above a lock and can be clicked, so an emergency
    /// alert sent to a locked room can still be answered.
    /// </summary>
    class LockManager
    {
        readonly Action<string, string> report;
        readonly AlertManager alerts;
        readonly List<LockData> active = new List<LockData>(); // oldest first; the newest is shown
        readonly List<Form> windows = new List<Form>();
        readonly Timer tick = new Timer();
        readonly Native.HookProc keyProc, mouseProc; // fields, so the GC cannot collect them while hooked
        IntPtr keyHook, mouseHook;
        LockData showing;
        Label countdown;

        public LockManager(Action<string, string> reportEvent, AlertManager alertManager)
        {
            report = reportEvent;
            alerts = alertManager;
            keyProc = OnKey;
            mouseProc = OnMouse;
            tick.Interval = 500;
            tick.Tick += delegate { Tick(); };
        }

        public bool Locked { get { return showing != null; } }

        public void Apply(LockData l)
        {
            LockData existing = active.Find(delegate(LockData x) { return x.Id == l.Id; });
            if (existing != null) existing.EndsUtc = l.EndsUtc; // the server re-sent it; keep the newer end time
            else active.Add(l);
            Log.Write("Locked by " + l.Sender + " (" + l.Mode + ") until " + l.EndsUtc.ToLocalTime().ToString("HH:mm"));
            Refresh();
            report(l.Id, "locked");
        }

        public void Release(string id)
        {
            int n = active.RemoveAll(delegate(LockData x) { return x.Id == id; });
            if (n == 0) return;
            Log.Write("Lock " + id + " lifted");
            Refresh();
            report(id, "released");
        }

        void Tick()
        {
            DateTime now = DateTime.UtcNow;
            foreach (LockData l in active.ToArray())
                if (l.EndsUtc <= now) Release(l.Id);
            if (showing == null) return;
            // Stay above everything that is not an alert; then put alerts back above us.
            foreach (Form w in windows) { try { Native.KeepOnTop(w.Handle); } catch { } }
            alerts.RaiseAll();
            if (countdown != null) countdown.Text = UntilText(showing);
        }

        void Refresh()
        {
            LockData want = active.Count > 0 ? active[active.Count - 1] : null;
            if (want == showing) return;
            CloseWindows();
            showing = want;
            if (want == null) { Unhook(); tick.Stop(); return; }
            try { Build(want); }
            catch (Exception ex) { Log.Write("Could not build lock screen: " + ex); }
            Hook();
            tick.Start();
        }

        // ---- windows ----

        void Build(LockData l)
        {
            foreach (Screen s in Screen.AllScreens)
            {
                Form f = l.Mode == "freeze" ? (Form)new Cover(s.Bounds, Color.Black, 0.01) : new Cover(s.Bounds, LockColour, 1.0);
                if (l.Mode != "freeze") FillBlank(f, l, s);
                windows.Add(f);
            }
            if (l.Mode == "freeze") windows.Add(Banner(l));
            foreach (Form f in windows) { try { f.Show(); Native.KeepOnTop(f.Handle); } catch { } }
            alerts.RaiseAll();
        }

        static readonly Color LockColour = Color.FromArgb(0x13, 0x21, 0x2c);

        static string UntilText(LockData l)
        {
            return "Locked by " + (l.Sender == "" ? "a teacher" : l.Sender) +
                   "   \u00b7   unlocks by itself at " + l.EndsUtc.ToLocalTime().ToString("HH:mm");
        }

        void FillBlank(Form f, LockData l, Screen s)
        {
            Rectangle b = s.Bounds;
            int column = Math.Min(Look.Px(900), (int)(b.Width * 0.8));
            ContentAlignment mid = ContentAlignment.MiddleCenter;
            var items = new List<Label>();
            Label icon = Look.Text("\U0001F512", Look.IconFont(s.Primary ? 54f : 40f), column, mid);
            icon.ForeColor = Color.White;
            items.Add(icon);
            Label head = Look.Text(l.Org == "" ? "This computer is locked" : l.Org + "  \u00b7  This computer is locked",
                Look.Font(s.Primary ? 14f : 12f, FontStyle.Bold), column, mid);
            head.ForeColor = Color.FromArgb(0xc9, 0xd4, 0xdc);
            items.Add(head);
            if (s.Primary)
            {
                Label msg = Look.Text(l.Message == "" ? "Eyes to the front, please." : l.Message,
                    Look.Font(l.Message.Length > 200 ? 18f : 28f, FontStyle.Bold), column, mid);
                msg.ForeColor = Color.White;
                items.Add(msg);
                countdown = Look.Text(UntilText(l), Look.Font(11f, FontStyle.Regular), column, mid);
                countdown.ForeColor = Color.FromArgb(0xc9, 0xd4, 0xdc);
                items.Add(countdown);
            }
            int total = 0, gap = Look.Px(18);
            foreach (Label x in items) total += x.Height + gap;
            int y = Math.Max(Look.Px(20), (b.Height - total) / 2), left = (b.Width - column) / 2;
            foreach (Label x in items)
            {
                x.BackColor = Color.Transparent;
                x.Location = new Point(left, y);
                f.Controls.Add(x);
                y += x.Height + gap;
            }
        }

        Form Banner(LockData l)
        {
            Rectangle area = Screen.PrimaryScreen.Bounds;
            int width = Math.Min(Look.Px(720), area.Width - Look.Px(40)), pad = Look.Px(16);
            var f = new Cover(Rectangle.Empty, LockColour, 1.0);
            var icon = new Label();
            icon.Text = "\U0001F512"; icon.Font = Look.IconFont(20f); icon.ForeColor = Color.White;
            icon.SetBounds(pad, pad, Look.Px(40), Look.Px(40));
            f.Controls.Add(icon);
            int textLeft = pad + Look.Px(48), textWidth = width - textLeft - pad, y = pad;
            Label head = Look.Text("Your keyboard and mouse are locked", Look.Font(12.5f, FontStyle.Bold), textWidth, ContentAlignment.TopLeft);
            head.ForeColor = Color.White; head.Location = new Point(textLeft, y);
            f.Controls.Add(head);
            y += head.Height;
            if (l.Message != "")
            {
                Label msg = Look.Text(l.Message, Look.Font(10.5f, FontStyle.Regular), textWidth, ContentAlignment.TopLeft);
                msg.ForeColor = Color.White; msg.Location = new Point(textLeft, y);
                f.Controls.Add(msg);
                y += msg.Height;
            }
            countdown = Look.Text(UntilText(l), Look.Font(9f, FontStyle.Regular), textWidth, ContentAlignment.TopLeft);
            countdown.ForeColor = Color.FromArgb(0xc9, 0xd4, 0xdc); countdown.Location = new Point(textLeft, y + Look.Px(2));
            f.Controls.Add(countdown);
            y += countdown.Height + pad;
            f.Bounds = new Rectangle(area.Left + (area.Width - width) / 2, area.Top + Look.Px(24), width, Math.Max(y, Look.Px(72)));
            return f;
        }

        void CloseWindows()
        {
            foreach (Form f in windows)
            {
                var c = f as Cover;
                if (c != null) c.Releasing = true;
                try { f.Close(); f.Dispose(); } catch { }
            }
            windows.Clear();
            countdown = null;
        }

        // ---- input hooks ----

        void Hook()
        {
            if (keyHook == IntPtr.Zero) keyHook = Native.Hook(Native.WH_KEYBOARD_LL, keyProc);
            if (mouseHook == IntPtr.Zero) mouseHook = Native.Hook(Native.WH_MOUSE_LL, mouseProc);
            if (keyHook == IntPtr.Zero || mouseHook == IntPtr.Zero)
                Log.Write("Could not hook " + (keyHook == IntPtr.Zero ? "the keyboard " : "") + (mouseHook == IntPtr.Zero ? "the mouse" : "") +
                          " (error " + Marshal.GetLastWin32Error() + "). The screen is still covered.");
        }

        void Unhook()
        {
            Native.Unhook(keyHook); keyHook = IntPtr.Zero;
            Native.Unhook(mouseHook); mouseHook = IntPtr.Zero;
        }

        // Hook callbacks run on this (the UI) thread and must return quickly:
        // Windows skips a hook that takes too long, which is the failsafe if
        // this process ever hangs.
        IntPtr OnKey(int nCode, IntPtr wParam, IntPtr lParam)
        {
            if (nCode >= 0 && showing != null) return (IntPtr)1; // swallowed
            return Native.CallNextHookEx(keyHook, nCode, wParam, lParam);
        }

        IntPtr OnMouse(int nCode, IntPtr wParam, IntPtr lParam)
        {
            if (nCode >= 0 && showing != null)
            {
                bool allow = false;
                if (alerts.AnyOpen)
                {
                    // Let the pointer move, and click only on alerts.
                    var info = (Native.MSLLHOOKSTRUCT)Marshal.PtrToStructure(lParam, typeof(Native.MSLLHOOKSTRUCT));
                    allow = (int)wParam == Native.WM_MOUSEMOVE || alerts.IsOverAlert(new Point(info.pt.X, info.pt.Y));
                }
                if (!allow) return (IntPtr)1;
            }
            return Native.CallNextHookEx(mouseHook, nCode, wParam, lParam);
        }

        /// <summary>A borderless, always-on-top window that never shows in the taskbar or Alt+Tab.</summary>
        class Cover : Form
        {
            /// <summary>Set when the lock itself is closing the window; any other close is refused.</summary>
            public bool Releasing;

            public Cover(Rectangle bounds, Color colour, double opacity)
            {
                FormBorderStyle = FormBorderStyle.None; ShowInTaskbar = false; ControlBox = false;
                StartPosition = FormStartPosition.Manual; AutoScaleMode = AutoScaleMode.None;
                BackColor = colour; ForeColor = Color.White;
                if (bounds != Rectangle.Empty) Bounds = bounds;
                if (opacity < 1.0) Opacity = opacity; // nearly invisible, but still catches every click and touch
                Text = "Foghorn lock";
            }
            protected override CreateParams CreateParams
            {
                get { CreateParams cp = base.CreateParams; cp.ExStyle |= Native.WS_EX_TOPMOST | Native.WS_EX_TOOLWINDOW; return cp; }
            }
            protected override bool ShowWithoutActivation { get { return true; } }
            protected override void OnFormClosing(FormClosingEventArgs e)
            {
                // Alt+F4 never gets here (the keyboard is hooked), but be sure.
                // Close() from our own code also reports UserClosing, hence the flag.
                if (!Releasing && e.CloseReason == CloseReason.UserClosing) { e.Cancel = true; return; }
                base.OnFormClosing(e);
            }
        }
    }
}
