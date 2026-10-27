using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Drawing;
using System.Windows.Forms;

namespace Foghorn
{
    /// <summary>Colours and wording shared by every alert style. Matches the preview in the web console.</summary>
    static class Look
    {
        public static readonly Color Ink = Color.FromArgb(0x13, 0x21, 0x2c);
        public static readonly Color Muted = Color.FromArgb(0x5a, 0x6b, 0x78);
        public static readonly Color Border = Color.FromArgb(0xb9, 0xc5, 0xcd);

        public static Color Band(string level)
        {
            if (level == "critical") return Color.FromArgb(0xc3, 0x30, 0x1c);
            if (level == "warning") return Color.FromArgb(0xc7, 0x77, 0x00);
            return Color.FromArgb(0x1d, 0x6f, 0xb8);
        }

        public static Color Curtain(string level)
        {
            if (level == "critical") return Color.FromArgb(0x7a, 0x1a, 0x0d);
            if (level == "warning") return Color.FromArgb(0x6b, 0x41, 0x00);
            return Color.FromArgb(0x10, 0x39, 0x5e);
        }

        public static string Word(string level)
        {
            if (level == "critical") return "Urgent";
            if (level == "warning") return "Warning";
            return "Notice";
        }

        /// <summary>"#1d6fb8" to a colour, or Empty if it is not one.</summary>
        public static Color Parse(string hex)
        {
            int v;
            if (hex == null || hex.Length != 7 || hex[0] != '#' ||
                !int.TryParse(hex.Substring(1), System.Globalization.NumberStyles.HexNumber, null, out v)) return Color.Empty;
            return Color.FromArgb((v >> 16) & 0xff, (v >> 8) & 0xff, v & 0xff);
        }

        public static Color Band(AlertData d)
        {
            Color c = Parse(d.Accent);
            return c.IsEmpty ? Band(d.Level) : c;
        }

        /// <summary>The full-screen background: the accent, darkened until white text reads well on it.</summary>
        public static Color Curtain(AlertData d)
        {
            Color c = Parse(d.Accent);
            if (c.IsEmpty) return Curtain(d.Level);
            double k = 0.55;
            return Color.FromArgb((int)(c.R * k), (int)(c.G * k), (int)(c.B * k));
        }

        public static string Word(AlertData d) { return d.Label != "" ? d.Label : Word(d.Level); }

        /// <summary>The builder's icons are emoji; this font has every one of them on Windows 10 and 11.</summary>
        public static Font IconFont(float points)
        {
            try { return new Font("Segoe UI Emoji", points, FontStyle.Regular, GraphicsUnit.Point); }
            catch { return Font(points, FontStyle.Regular); }
        }

        public static Font Font(float points, FontStyle style)
        {
            try { return new Font("Segoe UI", points, style, GraphicsUnit.Point); }
            catch { return new Font(SystemFonts.MessageBoxFont.FontFamily, points, style, GraphicsUnit.Point); }
        }

        static float scale;
        /// <summary>Pixels per design pixel (1.0 at 100% display scaling, 1.5 at 150%...).</summary>
        public static float Scale
        {
            get
            {
                if (scale > 0) return scale;
                try { using (Graphics g = Graphics.FromHwnd(IntPtr.Zero)) scale = g.DpiX / 96f; } catch { scale = 1f; }
                if (scale < 1f || scale > 5f) scale = 1f;
                return scale;
            }
        }

        /// <summary>
        /// A label sized by measuring its wrapped text ourselves. Label.AutoSize is
        /// not dependable for multi-line text, and an alert that cuts off the
        /// message is worse than no alert.
        /// </summary>
        public static Label Text(string text, Font font, int width, ContentAlignment align)
        {
            var l = new Label();
            l.UseMnemonic = false; l.AutoSize = false; l.Font = font; l.Text = text; l.TextAlign = align;
            l.Margin = Padding.Empty; l.Padding = Padding.Empty;
            l.Size = new Size(width, Measure(text, font, width));
            return l;
        }

        public static int Measure(string text, Font font, int width)
        {
            Size sz = TextRenderer.MeasureText(text == "" ? " " : text, font, new Size(width, 100000),
                TextFormatFlags.WordBreak | TextFormatFlags.NoPrefix);
            return sz.Height + Px(4);
        }

        public static int Px(int designPixels) { return (int)Math.Round(designPixels * Scale); }

        public static Button MakeButton(string text, bool primary, bool onDark, float points)
        {
            var b = new Button();
            b.Text = text;
            b.Font = Font(points, FontStyle.Bold);
            b.FlatStyle = FlatStyle.Flat;
            b.AutoSize = true;
            b.AutoSizeMode = AutoSizeMode.GrowAndShrink;
            b.Padding = new Padding(Px(12), Px(3), Px(12), Px(3));
            b.Cursor = Cursors.Hand;
            b.UseVisualStyleBackColor = false;
            b.TabStop = true;
            if (onDark)
            {
                b.BackColor = primary ? Color.White : Color.Empty; // Empty = take the curtain colour from the parent
                b.ForeColor = primary ? Ink : Color.White;
                b.FlatAppearance.BorderColor = Color.White;
                b.FlatAppearance.BorderSize = primary ? 0 : 1;
            }
            else
            {
                b.BackColor = primary ? Ink : Color.White;
                b.ForeColor = primary ? Color.White : Ink;
                b.FlatAppearance.BorderColor = primary ? Ink : Border;
                b.FlatAppearance.BorderSize = 1;
            }
            return b;
        }
    }

    /// <summary>
    /// Base for both alert styles: always on top, never in the taskbar or Alt+Tab,
    /// cannot be closed with Alt+F4 when an acknowledgement is required, and
    /// re-asserts "topmost" every second and a half in case another always-on-top
    /// window opens over it.
    /// </summary>
    abstract class AlertWindow : Form
    {
        public readonly AlertData Data;
        /// <summary>Raised once, with: acknowledged | dismissed | timeout | recalled.</summary>
        public event Action<AlertWindow, string> Finished;
        /// <summary>The reply button pressed, for alerts that ask a question.</summary>
        public string Choice = "";
        AlertSound sound;

        protected readonly bool TakesFocus;
        readonly Timer topmostTimer = new Timer();
        readonly Timer lifeTimer = new Timer();
        readonly Timer fadeTimer = new Timer();
        protected Panel CountdownBar;
        DateTime shownAt;
        bool finishing;

        protected AlertWindow(AlertData data, bool takesFocus)
        {
            Data = data;
            TakesFocus = takesFocus;
            FormBorderStyle = FormBorderStyle.None;
            ShowInTaskbar = false;
            StartPosition = FormStartPosition.Manual;
            AutoScaleMode = AutoScaleMode.None;
            ControlBox = false;
            MinimizeBox = false;
            MaximizeBox = false;
            Text = data.Title;
            DoubleBuffered = true;

            topmostTimer.Interval = 1500;
            topmostTimer.Tick += delegate { if (Visible && !IsDisposed) Native.KeepOnTop(Handle); };
            lifeTimer.Interval = 200;
            lifeTimer.Tick += delegate { TickLife(); };
            fadeTimer.Interval = 15;
            fadeTimer.Tick += delegate
            {
                double next = Opacity + 0.12;
                if (next >= 1) { next = 1; fadeTimer.Stop(); }
                try { Opacity = next; } catch { fadeTimer.Stop(); }
            };
        }

        // Do not set the TopMost property: WinForms then ignores
        // ShowWithoutActivation and the alert would steal the keyboard.
        protected override CreateParams CreateParams
        {
            get
            {
                CreateParams cp = base.CreateParams;
                cp.ExStyle |= Native.WS_EX_TOPMOST | Native.WS_EX_TOOLWINDOW;
                if (!TakesFocus)
                {
                    cp.ExStyle |= Native.WS_EX_NOACTIVATE;
                    cp.ClassStyle |= Native.CS_DROPSHADOW;
                }
                return cp;
            }
        }

        protected override bool ShowWithoutActivation { get { return !TakesFocus; } }

        protected override void OnShown(EventArgs e)
        {
            base.OnShown(e);
            shownAt = DateTime.UtcNow;
            Native.KeepOnTop(Handle);
            if (TakesFocus) { Native.BringToFront(Handle); Activate(); }
            topmostTimer.Start();
            if (Data.DisplaySeconds > 0 && !Data.RequireAck) lifeTimer.Start();
            if (Data.Sound) { sound = new AlertSound(Data, this); sound.Start(); }
        }

        protected void StartFade()
        {
            try { Opacity = 0; fadeTimer.Start(); } catch { }
        }

        void TickLife()
        {
            double total = Data.DisplaySeconds, gone = (DateTime.UtcNow - shownAt).TotalSeconds;
            if (CountdownBar != null && CountdownBar.Parent != null)
                CountdownBar.Width = (int)(CountdownBar.Parent.ClientSize.Width * Math.Max(0, 1 - gone / total));
            if (gone >= total) Finish("timeout");
        }

        /// <summary>Close for a reason. Safe to call more than once.</summary>
        public void Finish(string reason)
        {
            if (finishing) return;
            finishing = true;
            topmostTimer.Stop(); lifeTimer.Stop(); fadeTimer.Stop();
            if (sound != null) sound.Stop();
            Action<AlertWindow, string> handler = Finished;
            try { Close(); } catch { }
            if (handler != null) handler(this, reason);
        }

        protected override void OnFormClosing(FormClosingEventArgs e)
        {
            // Alt+F4 and friends: only allowed to count as "Dismiss" when no acknowledgement is needed.
            if (!finishing && e.CloseReason == CloseReason.UserClosing)
            {
                e.Cancel = true;
                if (!Data.RequireAck) BeginInvoke((MethodInvoker)delegate { Finish("dismissed"); });
                return;
            }
            base.OnFormClosing(e);
        }

        protected override void Dispose(bool disposing)
        {
            if (disposing) { topmostTimer.Dispose(); lifeTimer.Dispose(); fadeTimer.Dispose(); if (sound != null) sound.Dispose(); }
            base.Dispose(disposing);
        }

        protected string Footer
        {
            get { return (Data.Sender == "" ? "Sent" : "From " + Data.Sender) + " at " + Data.Created.ToString("HH:mm"); }
        }

        protected string CloseLabel
        {
            get
            {
                if (Data.DismissLabel != "") return Data.DismissLabel;
                return Data.RequireAck ? "I\u2019ve read this" : "Dismiss";
            }
        }

        protected bool AsksQuestion { get { return Data.Responses.Count > 0; } }

        /// <summary>The buttons that close the alert: one per reply, or the single close button.</summary>
        protected List<Button> CloseButtons(bool onDark, float points)
        {
            var list = new List<Button>();
            if (!AsksQuestion)
            {
                Button b = Look.MakeButton(CloseLabel, true, onDark, points);
                b.Click += delegate { CloseByButton(); };
                list.Add(b);
                return list;
            }
            for (int i = 0; i < Data.Responses.Count; i++)
            {
                string choice = Data.Responses[i];
                Button b = Look.MakeButton(choice, i == 0, onDark, points);
                b.Click += delegate { Choice = choice; Finish("acknowledged"); };
                list.Add(b);
            }
            return list;
        }

        protected bool HasLink
        {
            get
            {
                Uri u;
                return Data.Link != "" && Uri.TryCreate(Data.Link, UriKind.Absolute, out u) && (u.Scheme == "http" || u.Scheme == "https");
            }
        }

        protected void OpenLink()
        {
            if (!HasLink) return; // never hand anything but a web address to the shell
            try { Process.Start(new ProcessStartInfo(Data.Link) { UseShellExecute = true }); }
            catch (Exception ex) { Log.Write("Could not open link: " + ex.Message); }
        }

        protected void CloseByButton() { Finish(Data.RequireAck ? "acknowledged" : "dismissed"); }
    }

    /// <summary>The corner and centre styles: a white card with a coloured band.</summary>
    class CardAlert : AlertWindow
    {
        public CardAlert(AlertData data) : base(data, false)
        {
            bool big = data.Display == "center";
            int width = Look.Px(big ? 560 : 420), pad = Look.Px(big ? 22 : 16);
            int inner = width - pad * 2;
            BackColor = Color.White;
            ForeColor = Look.Ink;

            var band = new Panel();
            band.BackColor = Look.Band(data);
            band.SetBounds(0, 0, width, Look.Px(big ? 34 : 28));
            var org = new Label();
            org.Text = data.Org == "" ? "Foghorn" : data.Org;
            org.Font = Look.Font(big ? 9.5f : 8.5f, FontStyle.Bold);
            org.ForeColor = Color.White; org.AutoEllipsis = true; org.UseMnemonic = false;
            org.TextAlign = ContentAlignment.MiddleLeft;
            org.SetBounds(pad, 0, inner - Look.Px(140), band.Height);
            var word = new Label();
            word.Text = Look.Word(data);
            word.Font = org.Font; word.ForeColor = Color.White;
            word.TextAlign = ContentAlignment.MiddleRight;
            word.AutoEllipsis = true; word.UseMnemonic = false;
            word.SetBounds(width - pad - Look.Px(140), 0, Look.Px(140), band.Height);
            band.Controls.Add(org); band.Controls.Add(word);
            Controls.Add(band);

            int y = band.Bottom + Look.Px(big ? 16 : 12);

            int textLeft = pad, textWidth = inner;
            if (data.Icon != "")
            {
                int box = Look.Px(big ? 44 : 34);
                var icon = new Label();
                icon.Text = data.Icon; icon.UseMnemonic = false;
                icon.Font = Look.IconFont(big ? 22f : 17f);
                icon.ForeColor = Look.Band(data);
                icon.TextAlign = ContentAlignment.TopCenter;
                icon.SetBounds(pad - Look.Px(4), y - Look.Px(2), box, box);
                Controls.Add(icon);
                textLeft = pad + box + Look.Px(6);
                textWidth = inner - box - Look.Px(6);
            }
            Label title = Look.Text(data.Title, Look.Font(big ? 16f : 12.5f, FontStyle.Bold), textWidth, ContentAlignment.TopLeft);
            title.Location = new Point(textLeft, y);
            Controls.Add(title);
            y += title.Height + Look.Px(4);

            if (data.Message != "")
            {
                Font mf = Look.Font(big ? 11.5f : 10f, FontStyle.Regular);
                int limit = Look.Px(big ? 320 : 200);
                if (Look.Measure(data.Message, mf, inner) <= limit)
                {
                    Label msg = Look.Text(data.Message, mf, inner, ContentAlignment.TopLeft);
                    msg.Location = new Point(pad, y);
                    Controls.Add(msg);
                    y += msg.Height;
                }
                else
                {
                    // A very long message scrolls inside the card rather than running off the screen.
                    var scroll = new Panel();
                    scroll.AutoScroll = true;
                    scroll.SetBounds(pad, y, inner, limit);
                    Label msg = Look.Text(data.Message, mf, inner - SystemInformation.VerticalScrollBarWidth - Look.Px(6), ContentAlignment.TopLeft);
                    msg.Location = new Point(0, 0);
                    scroll.Controls.Add(msg);
                    Controls.Add(scroll);
                    y += limit;
                }
            }
            y += Look.Px(big ? 18 : 14);

            if (AsksQuestion)
            {
                // Replies get a row of their own, wrapping if the wording is long.
                var row = new FlowLayoutPanel();
                row.FlowDirection = FlowDirection.LeftToRight;
                row.WrapContents = true;
                row.SetBounds(pad, y, inner, 10);
                foreach (Button b in CloseButtons(false, big ? 10f : 9f))
                {
                    b.Margin = new Padding(0, 0, Look.Px(8), Look.Px(8));
                    b.MaximumSize = new Size(inner, 0);
                    row.Controls.Add(b);
                }
                row.Height = row.GetPreferredSize(new Size(inner, 0)).Height;
                Controls.Add(row);
                y += row.Height + Look.Px(2);
            }
            Button close = AsksQuestion ? null : CloseButtons(false, big ? 10f : 9f)[0];
            Size cs;
            if (close != null) cs = close.PreferredSize;
            else using (Button probe = Look.MakeButton("X", false, false, big ? 10f : 9f)) cs = probe.PreferredSize; // footer row height
            int left = width - pad;
            if (close != null)
            {
                Controls.Add(close);
                close.SetBounds(width - pad - cs.Width, y, cs.Width, cs.Height);
                left = close.Left;
            }
            if (HasLink)
            {
                Button link = Look.MakeButton("Open link", false, false, big ? 10f : 9f);
                link.Click += delegate { OpenLink(); };
                Controls.Add(link);
                Size ls = link.PreferredSize;
                link.SetBounds(left - Look.Px(8) - ls.Width, y, ls.Width, cs.Height);
                left = link.Left;
            }
            var foot = new Label();
            foot.Text = Footer; foot.UseMnemonic = false;
            foot.Font = Look.Font(8.5f, FontStyle.Regular);
            foot.ForeColor = Look.Muted; foot.AutoEllipsis = true;
            foot.TextAlign = ContentAlignment.MiddleLeft;
            foot.SetBounds(pad, y, Math.Max(10, left - pad - Look.Px(8)), cs.Height);
            Controls.Add(foot);
            y += cs.Height + pad;

            if (data.DisplaySeconds > 0 && !data.RequireAck)
            {
                CountdownBar = new Panel();
                CountdownBar.BackColor = Look.Band(data);
                CountdownBar.SetBounds(0, y - Look.Px(3), width, Look.Px(3));
                Controls.Add(CountdownBar);
            }
            ClientSize = new Size(width, y);
            StartFade();
        }

        protected override void OnHandleCreated(EventArgs e)
        {
            base.OnHandleCreated(e);
            Native.RoundCorners(Handle);
        }

        protected override void OnPaint(PaintEventArgs e)
        {
            base.OnPaint(e);
            using (var pen = new Pen(Look.Border))
                e.Graphics.DrawRectangle(pen, 0, 0, ClientSize.Width - 1, ClientSize.Height - 1);
        }

        // Clicking the card must not pull focus away from what the person is typing in.
        protected override void WndProc(ref Message m)
        {
            if (m.Msg == Native.WM_MOUSEACTIVATE) { m.Result = (IntPtr)Native.MA_NOACTIVATE; return; }
            base.WndProc(ref m);
        }
    }

    /// <summary>The full-screen style: covers the primary monitor with the message and every other monitor with a plain curtain.</summary>
    class FullScreenAlert : AlertWindow
    {
        readonly List<Form> curtains = new List<Form>();

        public FullScreenAlert(AlertData data) : base(data, true)
        {
            Rectangle screen = Screen.PrimaryScreen.Bounds;
            Bounds = screen;
            BackColor = Look.Curtain(data);
            ForeColor = Color.White;
            KeyPreview = true;

            int column = Math.Min(Look.Px(980), (int)(screen.Width * 0.82));
            var items = new List<Control>();
            ContentAlignment mid = ContentAlignment.MiddleCenter;

            Label word = Look.Text((data.Org == "" ? "" : data.Org + "   ") + Look.Word(data), Look.Font(13f, FontStyle.Bold), column, mid);
            word.ForeColor = Color.FromArgb(225, 225, 225);
            if (data.Icon != "")
            {
                Label icon = Look.Text(data.Icon, Look.IconFont(48f), column, mid);
                icon.ForeColor = Color.White;
                items.Add(icon);
            }
            items.Add(word);
            items.Add(Look.Text(data.Title, Look.Font(screen.Width < Look.Px(1100) ? 26f : 36f, FontStyle.Bold), column, mid));
            if (data.Message != "")
                items.Add(Look.Text(data.Message, Look.Font(data.Message.Length > 600 ? 12f : 17f, FontStyle.Regular), column, mid));
            Label foot = Look.Text(Footer, Look.Font(11f, FontStyle.Regular), column, mid);
            foot.ForeColor = Color.FromArgb(215, 215, 215);
            items.Add(foot);

            int total = 0;
            int[] gaps = data.Icon != ""
                ? new[] { Look.Px(6), Look.Px(10), Look.Px(22), Look.Px(30), Look.Px(26) }
                : new[] { Look.Px(10), Look.Px(22), Look.Px(30), Look.Px(26) };
            foreach (Control c in items) total += c.Height;
            var buttons = new FlowLayoutPanel();
            buttons.AutoSize = true; buttons.AutoSizeMode = AutoSizeMode.GrowAndShrink;
            buttons.WrapContents = false;
            List<Button> closers = CloseButtons(true, 14f);
            foreach (Button b in closers) b.Margin = new Padding(Look.Px(8));
            Button close = closers[0];
            if (HasLink)
            {
                Button link = Look.MakeButton("Open link", false, true, 14f);
                link.Margin = new Padding(Look.Px(8));
                link.Click += delegate { OpenLink(); };
                buttons.Controls.Add(link);
            }
            foreach (Button b in closers) buttons.Controls.Add(b);
            Controls.Add(buttons);
            Size bs = buttons.PreferredSize;

            for (int i = 0; i < items.Count; i++) total += gaps[Math.Min(i, gaps.Length - 1)];
            total += bs.Height;

            int y = Math.Max(Look.Px(20), (screen.Height - total) / 2);
            int x = (screen.Width - column) / 2;
            for (int i = 0; i < items.Count; i++)
            {
                items[i].Location = new Point(x, y);
                Controls.Add(items[i]);
                y += items[i].Height + gaps[Math.Min(i, gaps.Length - 1)];
            }
            buttons.Location = new Point((screen.Width - bs.Width) / 2, y);
            ActiveControl = close;

            if (data.DisplaySeconds > 0 && !data.RequireAck)
            {
                CountdownBar = new Panel();
                CountdownBar.BackColor = Color.White;
                CountdownBar.SetBounds(0, screen.Height - Look.Px(5), screen.Width, Look.Px(5));
                Controls.Add(CountdownBar);
            }

            foreach (Screen s in Screen.AllScreens)
            {
                if (s.Primary) continue;
                var curtain = new Curtain(Look.Curtain(data), s.Bounds);
                curtains.Add(curtain);
            }
            Finished += delegate { foreach (Form c in curtains) { try { c.Close(); c.Dispose(); } catch { } } };
        }

        protected override void OnShown(EventArgs e)
        {
            foreach (Form c in curtains) { try { c.Show(); Native.KeepOnTop(c.Handle); } catch { } }
            base.OnShown(e);
        }

        class Curtain : Form
        {
            public Curtain(Color colour, Rectangle bounds)
            {
                FormBorderStyle = FormBorderStyle.None; ShowInTaskbar = false; ControlBox = false;
                StartPosition = FormStartPosition.Manual; AutoScaleMode = AutoScaleMode.None;
                BackColor = colour; Bounds = bounds;
            }
            protected override CreateParams CreateParams
            {
                get { CreateParams cp = base.CreateParams; cp.ExStyle |= Native.WS_EX_TOPMOST | Native.WS_EX_TOOLWINDOW | Native.WS_EX_NOACTIVATE; return cp; }
            }
            protected override bool ShowWithoutActivation { get { return true; } }
        }
    }

    /// <summary>
    /// Decides what is on screen. Runs on the UI thread only.
    /// Corner cards stack down the right-hand edge; centre cards cascade; one
    /// full-screen alert shows at a time. Anything that does not fit waits its turn.
    /// </summary>
    class AlertManager
    {
        const int MaxCards = 4;
        readonly Action<string, string, string> report;
        readonly Dictionary<string, AlertWindow> open = new Dictionary<string, AlertWindow>();
        readonly List<AlertWindow> order = new List<AlertWindow>();
        readonly List<AlertData> waiting = new List<AlertData>();
        readonly HashSet<string> seen = new HashSet<string>();

        public AlertManager(Action<string, string, string> reportEvent) { report = reportEvent; }

        /// <summary>
        /// True when the point is over an alert on screen. A screen lock uses
        /// this to let clicks through to alerts, so an emergency alert sent to
        /// a locked room can still be answered.
        /// </summary>
        public bool IsOverAlert(Point p)
        {
            foreach (AlertWindow w in order)
            {
                try { if (w.Visible && w.Bounds.Contains(p)) return true; } catch { }
            }
            return false;
        }

        public bool AnyOpen { get { return order.Count > 0; } }

        /// <summary>Put every open alert back on top, above a screen lock.</summary>
        public void RaiseAll()
        {
            foreach (AlertWindow w in order) { try { if (w.Visible) Native.KeepOnTop(w.Handle); } catch { } }
        }

        public void Show(AlertData a)
        {
            if (seen.Contains(a.Id))
            {
                // The server is asking again because our "displayed" receipt never arrived. Send it again.
                if (open.ContainsKey(a.Id)) report(a.Id, "displayed", "");
                return;
            }
            seen.Add(a.Id);
            if (!HasRoom(a)) { waiting.Add(a); return; }
            Open(a);
        }

        public void Recall(string id)
        {
            waiting.RemoveAll(delegate(AlertData w) { return w.Id == id; });
            AlertWindow w2;
            if (open.TryGetValue(id, out w2)) w2.Finish("recalled");
        }

        bool HasRoom(AlertData a)
        {
            int cards = 0; bool full = false;
            foreach (AlertWindow w in order) { if (w is FullScreenAlert) full = true; else cards++; }
            if (a.Display == "fullscreen") return !full;
            return cards < MaxCards;
        }

        void Open(AlertData a)
        {
            AlertWindow w;
            try
            {
                if (a.Display == "fullscreen") w = new FullScreenAlert(a); else w = new CardAlert(a);
            }
            catch (Exception ex) { Log.Write("Could not build alert window: " + ex); return; }
            w.Finished += OnFinished;
            open[a.Id] = w;
            order.Add(w);
            Arrange();
            try { w.Show(); } catch (Exception ex) { Log.Write("Could not show alert: " + ex); }
            Log.Write("Showing \"" + a.Title + "\" (" + a.Level + ", " + a.Display + ")");
            report(a.Id, "displayed", "");
        }

        void OnFinished(AlertWindow w, string reason)
        {
            open.Remove(w.Data.Id);
            order.Remove(w);
            Log.Write("Closed \"" + w.Data.Title + "\": " + reason + (w.Choice != "" ? " (" + w.Choice + ")" : ""));
            report(w.Data.Id, reason, w.Choice);
            try { w.Dispose(); } catch { }
            for (int i = 0; i < waiting.Count; i++)
            {
                if (!HasRoom(waiting[i])) continue;
                AlertData next = waiting[i];
                waiting.RemoveAt(i);
                Open(next);
                i--;
            }
            Arrange();
        }

        void Arrange()
        {
            Rectangle area = Screen.PrimaryScreen.WorkingArea;
            int margin = Look.Px(16), y = area.Top + margin, centred = 0;
            foreach (AlertWindow w in order)
            {
                if (w is FullScreenAlert) continue;
                if (w.Data.Display == "center")
                {
                    int shift = Look.Px(26) * centred++;
                    w.Location = new Point(area.Left + (area.Width - w.Width) / 2 + shift,
                                           Math.Max(area.Top, area.Top + (int)((area.Height - w.Height) * 0.42) + shift));
                }
                else
                {
                    w.Location = new Point(area.Right - w.Width - margin, y);
                    y += w.Height + Look.Px(12);
                }
            }
        }
    }
}
