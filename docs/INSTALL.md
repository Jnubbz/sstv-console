# Installing SSTV Console

Your Mac or Windows PC will probably warn you the first time you open SSTV Console. This page
walks through it, step by step, and explains what the warning means.

**Get the app only from the
[Releases page](https://github.com/Jnubbz/sstv-console/releases/latest)** of this repository.
Don't use copies from anywhere else.

## What the warning is, and isn't

Apple and Microsoft each run a check on downloaded apps. An app passes quietly if its maker
has paid for an identity with them (Apple's Developer Program is $99 a year; a Windows
code-signing certificate is a yearly cost too) and sent each build in for
their automated scan. SSTV Console is a free hobby project and hasn't done that yet.

So the warning means **"nobody has registered this app with us"**. It does **not** mean the
scan found anything wrong with SSTV Console, and no malware was detected. It is also not an
endorsement: the system is telling you it hasn't vouched for the app, and the choice is
yours. That's why everything you need to judge it yourself is public:
[how to check what you downloaded and read the code](../SECURITY.md).

## Mac (Apple Silicon: M1, M2, M3, M4 and newer)

Intel Macs aren't supported yet.

1. Download `SSTV-Console-Mac.zip` from the Releases page. It unzips itself when you
   double-click it in your Downloads folder.
2. Drag **SSTV Console** into your **Applications** folder.
3. Open it from Applications. macOS shows a message such as *"Apple could not verify
   'SSTV Console' is free of malware"*. Click **Done**. (Don't click *Move to Trash*, which is
   the highlighted button.)
4. Open **System Settings** (Apple menu, top left) and choose **Privacy & Security** in the
   sidebar.
5. Scroll down to the **Security** section. You'll see a line saying *"SSTV Console" was
   blocked to protect your Mac* with an **Open Anyway** button. Click it.
6. Enter your Mac's password or use Touch ID, then click **Open** in the last dialog.

That's a one-time step: after this SSTV Console opens normally, and it updates itself. If
you don't see the "Open Anyway" line, double-click the app again, let it be blocked, and then
look in Privacy & Security. The wording varies a little between macOS versions.

On older macOS (Sonoma 14 and earlier) you can instead right-click the app, choose **Open**,
and click **Open** again. Apple removed that shortcut in macOS Sequoia (15), and Tahoe (26)
doesn't have it either, which is why there are more steps now.

The first time you turn on audio, macOS asks for microphone access. SSTV Console needs
that to hear the radio's sound card. It doesn't record anything unless you press
*Record 60 seconds* in the Radio panel.

### If macOS says the app is "damaged"

That message can mean an interrupted download. Delete the file, download it again, and compare its
SHA-256 with the one in `latest.json` ([how](../SECURITY.md#check-your-download)). If they match,
the file is intact and the "damaged" wording is just Apple's name for "not registered": use
the steps above. As an alternative to step 4 to 6, Terminal does the same thing for this one app:

    xattr -dr com.apple.quarantine "/Applications/SSTV Console.app"

That removes only the "downloaded from the internet" flag from SSTV Console. It doesn't
turn anything off for the rest of your Mac. Don't use commands that disable Gatekeeper
altogether: you don't need to.

## Windows (10 and 11, 64-bit)

1. Download `SSTV-Console-Windows.zip` from the Releases page.
2. Right-click the zip, choose **Extract All**, and extract to somewhere you can write to,
   such as Documents or the Desktop. (Not *Program Files*: the app updates itself in place.)
3. Open the folder and double-click `SSTV-Console.exe`.
4. A blue window says **Windows protected your PC**, and *Microsoft Defender SmartScreen
   prevented an unrecognized app from starting*. Click the small **More info** link in the
   text. The publisher shows as *Unknown publisher*, which is expected: see above.
5. A **Run anyway** button appears. Click it.

This is also one-time. If there is no **Run anyway** button, close the window, right-click the
**zip file** (before extracting), choose **Properties**, tick **Unblock** at the bottom of the
General tab, click OK, and extract it again.

If your antivirus program quarantines the file instead, check that the file you downloaded
matches the SHA-256 in `latest.json`, then add an exception for the SSTV Console folder.
Unsigned programs sometimes get flagged by mistake; the code is open for you to check.

## After installing

- The app runs a small local web page and shows it in its own window. Nothing is reachable
  from other computers.
- It checks GitHub for updates a few seconds after starting and every 6 hours. About →
  *Check for updates* does it right away. Updates are installed only after their signature
  and checksum pass.
- Something not working? About → **Report a bug** opens a prefilled GitHub issue.

## When this goes away

Registering with Apple and getting the app notarized fixes the Mac warning. A Windows
certificate fixes the Windows one. Both are paid, so they'll happen if there's demand and
someone sponsors it. Until then, these steps are the way in.
