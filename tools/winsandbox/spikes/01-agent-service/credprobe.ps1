# Writes or reads a generic credential in the Credential Manager of the
# current account (dummy value, no real secret). Used by spike 3.
param([ValidateSet('write', 'read')][string]$Mode, [string]$Target = 'mestre-spike-probe')
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class CredProbe {
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    public struct CREDENTIAL {
        public int Flags; public int Type; public string TargetName; public string Comment;
        public long LastWritten; public int CredentialBlobSize; public IntPtr CredentialBlob;
        public int Persist; public int AttributeCount; public IntPtr Attributes;
        public string TargetAlias; public string UserName;
    }
    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern bool CredWrite(ref CREDENTIAL c, int flags);
    [DllImport("advapi32.dll", CharSet = CharSet.Unicode, SetLastError = true)]
    public static extern bool CredRead(string target, int type, int flags, out IntPtr c);
    [DllImport("advapi32.dll")] public static extern void CredFree(IntPtr p);
    public static string Write(string target, string value) {
        var b = System.Text.Encoding.Unicode.GetBytes(value);
        var c = new CREDENTIAL { Type = 1, TargetName = target, UserName = "probe", Persist = 2, CredentialBlobSize = b.Length };
        c.CredentialBlob = Marshal.AllocHGlobal(b.Length);
        Marshal.Copy(b, 0, c.CredentialBlob, b.Length);
        bool ok = CredWrite(ref c, 0);
        int err = Marshal.GetLastWin32Error();
        Marshal.FreeHGlobal(c.CredentialBlob);
        return ok ? "CredWrite ok" : "CredWrite failed, error " + err;
    }
    public static string Read(string target) {
        IntPtr p;
        if (!CredRead(target, 1, 0, out p)) return "CredRead failed, error " + Marshal.GetLastWin32Error();
        var c = (CREDENTIAL)Marshal.PtrToStructure(p, typeof(CREDENTIAL));
        string v = Marshal.PtrToStringUni(c.CredentialBlob, c.CredentialBlobSize / 2);
        CredFree(p);
        return "CredRead ok, value=" + v;
    }
}
'@
if ($Mode -eq 'write') { [CredProbe]::Write($Target, "probe-" + (Get-Date -Format o)) } else { [CredProbe]::Read($Target) }
