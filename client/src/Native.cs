using System;
using System.Runtime.InteropServices;

namespace Foghorn
{
    /// <summary>
    /// The handful of Windows API calls WinForms does not expose. Every call is
    /// wrapped so that a failure (or running under Mono for testing) is harmless.
    /// </summary>
    static class Native
    {
        public const int WS_EX_TOPMOST = 0x00000008;
        public const int WS_EX_TOOLWINDOW = 0x00000080;
        public const int WS_EX_NOACTIVATE = 0x08000000;
        public const int CS_DROPSHADOW = 0x00020000;
        public const int WM_MOUSEACTIVATE = 0x0021;
        public const int MA_NOACTIVATE = 3;

        static readonly IntPtr HWND_TOPMOST = new IntPtr(-1);
        const uint SWP_NOSIZE = 0x0001, SWP_NOMOVE = 0x0002, SWP_NOACTIVATE = 0x0010, SWP_NOOWNERZORDER = 0x0200;

        [DllImport("user32.dll")]
        static extern bool SetProcessDPIAware();

        [DllImport("user32.dll")]
        static extern bool SetWindowPos(IntPtr hWnd, IntPtr after, int x, int y, int cx, int cy, uint flags);

        [DllImport("user32.dll")]
        static extern bool SetForegroundWindow(IntPtr hWnd);

        [DllImport("dwmapi.dll")]
        static extern int DwmSetWindowAttribute(IntPtr hwnd, int attr, ref int value, int size);

        /// <summary>Without this, Windows bitmap-stretches the alert on high-DPI screens and the text goes blurry.</summary>
        public static void MakeDpiAware()
        {
            try { SetProcessDPIAware(); } catch { }
        }

        /// <summary>Push the window back above everything else without taking focus.</summary>
        public static void KeepOnTop(IntPtr hwnd)
        {
            try { SetWindowPos(hwnd, HWND_TOPMOST, 0, 0, 0, 0, SWP_NOMOVE | SWP_NOSIZE | SWP_NOACTIVATE | SWP_NOOWNERZORDER); } catch { }
        }

        public static void BringToFront(IntPtr hwnd)
        {
            try { SetForegroundWindow(hwnd); } catch { }
        }

        /// <summary>Windows 11 rounded corners. Older Windows returns an error code, which we ignore.</summary>
        public static void RoundCorners(IntPtr hwnd)
        {
            try { int round = 2; DwmSetWindowAttribute(hwnd, 33, ref round, sizeof(int)); } catch { }
        }

        // ---- input hooks, for screen locks ----
        //
        // Low-level hooks see every key press and mouse event in the session
        // before any window does, and work without administrator rights. They
        // cannot see Ctrl+Alt+Del: Windows reserves that, which is what keeps
        // "sign out" always available to the person at the PC.

        public const int WH_KEYBOARD_LL = 13, WH_MOUSE_LL = 14;
        public const int WM_MOUSEMOVE = 0x0200;

        public delegate IntPtr HookProc(int nCode, IntPtr wParam, IntPtr lParam);

        [StructLayout(LayoutKind.Sequential)]
        public struct POINT { public int X, Y; }

        [StructLayout(LayoutKind.Sequential)]
        public struct MSLLHOOKSTRUCT { public POINT pt; public uint mouseData, flags, time; public IntPtr extra; }

        [DllImport("user32.dll", SetLastError = true)]
        static extern IntPtr SetWindowsHookEx(int idHook, HookProc fn, IntPtr hMod, uint threadId);

        [DllImport("user32.dll")]
        static extern bool UnhookWindowsHookEx(IntPtr hook);

        [DllImport("user32.dll")]
        public static extern IntPtr CallNextHookEx(IntPtr hook, int nCode, IntPtr wParam, IntPtr lParam);

        [DllImport("kernel32.dll", CharSet = CharSet.Unicode)]
        static extern IntPtr GetModuleHandle(string name);

        /// <summary>Installs a low-level hook for this process. Returns IntPtr.Zero on failure.</summary>
        public static IntPtr Hook(int kind, HookProc fn)
        {
            try
            {
                using (var p = System.Diagnostics.Process.GetCurrentProcess())
                using (var m = p.MainModule)
                    return SetWindowsHookEx(kind, fn, GetModuleHandle(m.ModuleName), 0);
            }
            catch { return IntPtr.Zero; }
        }

        public static void Unhook(IntPtr hook)
        {
            if (hook == IntPtr.Zero) return;
            try { UnhookWindowsHookEx(hook); } catch { }
        }

        // ---- MCI, for playing MP3s (SoundPlayer only does WAV) ----

        [DllImport("winmm.dll", CharSet = CharSet.Unicode)]
        static extern int mciSendString(string command, System.Text.StringBuilder ret, int retLen, IntPtr callback);

        /// <summary>Sends an MCI command; returns true if it worked.</summary>
        public static bool Mci(string command)
        {
            try { return mciSendString(command, null, 0, IntPtr.Zero) == 0; } catch { return false; }
        }

        // ---- starting the desktop client in people's sessions, for the updater ----
        //
        // The updater runs as SYSTEM in session 0. After an update it starts the
        // new client in each signed-in session, as that session's user, on their
        // desktop - the same as the Run key would at their next logon.

        [StructLayout(LayoutKind.Sequential)]
        struct WTS_SESSION_INFO { public int SessionId; public IntPtr WinStationName; public int State; }

        [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
        struct STARTUPINFO
        {
            public int cb; public string lpReserved, lpDesktop, lpTitle;
            public int dwX, dwY, dwXSize, dwYSize, dwXCountChars, dwYCountChars, dwFillAttribute, dwFlags;
            public short wShowWindow, cbReserved2; public IntPtr lpReserved2, hStdInput, hStdOutput, hStdError;
        }

        [StructLayout(LayoutKind.Sequential)]
        struct PROCESS_INFORMATION { public IntPtr hProcess, hThread; public int dwProcessId, dwThreadId; }

        [DllImport("wtsapi32.dll", SetLastError = true)]
        static extern bool WTSEnumerateSessions(IntPtr server, int reserved, int version, out IntPtr sessions, out int count);

        [DllImport("wtsapi32.dll")]
        static extern void WTSFreeMemory(IntPtr memory);

        [DllImport("wtsapi32.dll", SetLastError = true)]
        static extern bool WTSQueryUserToken(int sessionId, out IntPtr token);

        [DllImport("advapi32.dll", SetLastError = true)]
        static extern bool DuplicateTokenEx(IntPtr token, int access, IntPtr attributes, int impersonationLevel, int tokenType, out IntPtr newToken);

        [DllImport("userenv.dll", SetLastError = true)]
        static extern bool CreateEnvironmentBlock(out IntPtr environment, IntPtr token, bool inherit);

        [DllImport("userenv.dll")]
        static extern bool DestroyEnvironmentBlock(IntPtr environment);

        [DllImport("advapi32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
        static extern bool CreateProcessAsUser(IntPtr token, string application, string commandLine, IntPtr processAttributes,
            IntPtr threadAttributes, bool inheritHandles, int flags, IntPtr environment, string currentDirectory,
            ref STARTUPINFO startup, out PROCESS_INFORMATION info);

        [DllImport("kernel32.dll")]
        static extern bool CloseHandle(IntPtr handle);

        /// <summary>Sessions with someone signed in: active, or disconnected but still signed in.</summary>
        public static System.Collections.Generic.List<int> UserSessions()
        {
            var list = new System.Collections.Generic.List<int>();
            IntPtr buffer; int count;
            try
            {
                if (!WTSEnumerateSessions(IntPtr.Zero, 0, 1, out buffer, out count)) return list;
                int size = Marshal.SizeOf(typeof(WTS_SESSION_INFO));
                for (int i = 0; i < count; i++)
                {
                    var s = (WTS_SESSION_INFO)Marshal.PtrToStructure(new IntPtr(buffer.ToInt64() + i * size), typeof(WTS_SESSION_INFO));
                    // 0 = WTSActive, 4 = WTSDisconnected. Session 0 is services.
                    if (s.SessionId != 0 && (s.State == 0 || s.State == 4)) list.Add(s.SessionId);
                }
                WTSFreeMemory(buffer);
            }
            catch { }
            return list;
        }

        /// <summary>Starts exe as the user signed in to session. Returns null, or what went wrong.</summary>
        public static string StartInSession(int session, string exe)
        {
            IntPtr userToken = IntPtr.Zero, primary = IntPtr.Zero, env = IntPtr.Zero;
            try
            {
                if (!WTSQueryUserToken(session, out userToken)) return "no user token (error " + Marshal.GetLastWin32Error() + ")";
                // MAXIMUM_ALLOWED, SecurityImpersonation, TokenPrimary
                if (!DuplicateTokenEx(userToken, 0x02000000, IntPtr.Zero, 2, 1, out primary)) return "cannot use the token (error " + Marshal.GetLastWin32Error() + ")";
                CreateEnvironmentBlock(out env, primary, false);
                var si = new STARTUPINFO();
                si.cb = Marshal.SizeOf(typeof(STARTUPINFO));
                si.lpDesktop = @"winsta0\default";
                PROCESS_INFORMATION pi;
                const int CREATE_UNICODE_ENVIRONMENT = 0x400;
                if (!CreateProcessAsUser(primary, exe, "\"" + exe + "\"", IntPtr.Zero, IntPtr.Zero, false, CREATE_UNICODE_ENVIRONMENT,
                        env, System.IO.Path.GetDirectoryName(exe), ref si, out pi))
                    return "could not start it (error " + Marshal.GetLastWin32Error() + ")";
                CloseHandle(pi.hThread); CloseHandle(pi.hProcess);
                return null;
            }
            catch (Exception ex) { return ex.Message; }
            finally
            {
                if (env != IntPtr.Zero) DestroyEnvironmentBlock(env);
                if (primary != IntPtr.Zero) CloseHandle(primary);
                if (userToken != IntPtr.Zero) CloseHandle(userToken);
            }
        }
    }
}
