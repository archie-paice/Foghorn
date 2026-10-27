using System;
using System.Media;
using System.Threading;
using System.Windows.Forms;

namespace Foghorn
{
    /// <summary>
    /// The sound for one alert: an uploaded WAV or MP3 if it has one, otherwise
    /// the Windows sound for its level. Plays once, or until Stop() when the
    /// alert asks for a loop. Every call is on the UI thread; the download, if
    /// one is needed, happens on a worker so the alert itself is never held up.
    /// </summary>
    class AlertSound : IDisposable
    {
        static int counter;

        readonly AlertData data;
        readonly Control owner;
        readonly System.Windows.Forms.Timer repeat = new System.Windows.Forms.Timer();
        SoundPlayer player;
        string mciAlias;
        bool stopped;

        public AlertSound(AlertData data, Control owner)
        {
            this.data = data;
            this.owner = owner;
            repeat.Interval = 2500;
            repeat.Tick += delegate { PlaySystem(); };
        }

        public void Start()
        {
            if (data.SoundId == "") { StartSystem(); return; }
            var t = new Thread(delegate()
            {
                string path = Poller.FetchSound(data.SoundId, data.SoundExt);
                try
                {
                    owner.BeginInvoke((MethodInvoker)delegate
                    {
                        if (stopped) return;
                        if (path == "" || !PlayFile(path)) StartSystem();
                    });
                }
                catch { /* the alert closed while we were downloading */ }
            });
            t.IsBackground = true;
            t.Name = "Foghorn sound";
            t.Start();
        }

        void StartSystem()
        {
            PlaySystem();
            if (data.SoundLoop) repeat.Start();
        }

        void PlaySystem()
        {
            try
            {
                if (data.Level == "critical") SystemSounds.Hand.Play();
                else if (data.Level == "warning") SystemSounds.Exclamation.Play();
                else SystemSounds.Asterisk.Play();
            }
            catch { }
        }

        bool PlayFile(string path)
        {
            try
            {
                if (path.EndsWith(".wav", StringComparison.OrdinalIgnoreCase))
                {
                    player = new SoundPlayer(path);
                    if (data.SoundLoop) player.PlayLooping(); else player.Play();
                    return true;
                }
                // MP3 through MCI, which is part of Windows. The path comes from
                // our own cache folder and a hex id, so it holds no quotes.
                mciAlias = "foghorn" + Interlocked.Increment(ref counter);
                if (!Native.Mci("open \"" + path + "\" type mpegvideo alias " + mciAlias)) { mciAlias = null; return false; }
                if (!Native.Mci("play " + mciAlias + (data.SoundLoop ? " repeat" : ""))) { Stop(); return false; }
                return true;
            }
            catch (Exception ex)
            {
                Log.Write("Could not play sound: " + ex.Message);
                return false;
            }
        }

        public void Stop()
        {
            stopped = true;
            repeat.Stop();
            if (player != null) { try { player.Stop(); player.Dispose(); } catch { } player = null; }
            if (mciAlias != null) { Native.Mci("stop " + mciAlias); Native.Mci("close " + mciAlias); mciAlias = null; }
        }

        public void Dispose()
        {
            Stop();
            repeat.Dispose();
        }
    }
}
