# Sending alerts with Foghorn — a guide for staff

Foghorn puts a message on people's computer screens, on top of whatever they
are doing, within a second or two of you pressing **Send alert**.

You use it from a web page. Your IT team will give you the address, a username
and a temporary password. The first time you sign in you choose your own
password (at least 10 characters).

> Every alert shows **your name** on it. Never share your sign-in.

---

## Send an alert in five steps

1. Open the Foghorn address in your browser and sign in. You land on **Send an alert**.
2. Type a **Title**. Make it make sense on its own — some people will read nothing else.
3. Type a **Message** if people need detail or need to do something.
4. Under **Who gets it**, choose the audience (see below).
5. Press **Send alert**. You are shown what you are about to send and how many
   computers it will reach. Press **Send alert** again to confirm.

The picture on the right of the page, **What people will see**, updates as you
type, so you always know exactly what will appear.

After sending you are taken to a page that shows, live, which computers have
displayed the alert.

---

## Who gets it

| Choice | Sends to |
|---|---|
| **Everyone** | Every computer with someone logged on. |
| **Groups** | One or more saved groups — typically rooms ("Lab 1"), departments or year groups. Tick the ones you want. Each shows how many of its computers are on right now. |
| **Specific computers or people** | Type a computer name, a username, or a pattern. `LAB1-*` means every computer whose name starts with `LAB1-`. Separate several with commas. |

Below your choice, a line tells you how many computers it reaches right now,
for example *"Reaches 23 computers that are on right now"*. Hover over it to
see some of their names. **If it says zero, nobody will see your alert** —
check your choice.

Some accounts are set up to send only to certain groups (your own classroom,
say). If yours is, you will only see those groups.

Foghorn reaches computers where someone is **logged on**. A computer sitting at
the Windows sign-in screen shows nothing.

---

## How serious, and where it appears

**How serious is it?** sets the colour and the word at the top of the alert.

| | Colour | Use for |
|---|---|---|
| **Notice** | Blue | Routine information. "This session ends in 5 minutes." |
| **Warning** | Amber | Something people must act on. "This PC will restart at 3:30." |
| **Urgent** | Red | Emergencies. |

**Where it appears** sets how hard it is to ignore.

| | What happens | Use for |
|---|---|---|
| **Corner** | A card slides in at the top-right. It stays above other windows but does not interrupt typing. | Most things. |
| **Centre** | A larger card in the middle of the screen. | Things nobody should miss. |
| **Full screen** | Covers the whole screen (every monitor) until closed. | Emergencies, and "eyes to the front, please". |

Use **Urgent** and **Full screen** sparingly. If they turn up every day, people
stop reacting to them.

---

## Options

**Make people confirm they have read it** — replaces the *Dismiss* button with
*I've read this*. The alert cannot be closed any other way, and Foghorn records
who pressed it. If someone logs off without pressing it, the alert comes back
the next time they log on (for as long as the alert is still being delivered —
see *late arrivals* below).

**Play a sound** — plays the standard Windows alert sound. Only heard if the
computer has speakers or headphones and is not muted, so do not rely on it.

**How long it stays on screen** — by default, until the person closes it. Or
choose a time after which it closes itself; a thin bar along the bottom of the
alert shows the time running out. (Not available together with "confirm they
have read it".)

**Also show it to people who log on later?** — by default, anyone who logs on
to a matching computer in the next 10 minutes sees it too.
- Choose **No – only computers on right now** for messages that are pointless
  later, like "eyes to the front".
- Choose a longer time for things that matter all day, like a room closure.

**Link (optional)** — adds an *Open link* button that opens a web page in the
person's browser. Must start with `http://` or `https://`.

**Play a sound.** Choose the Windows alert sound or one your administrators
have uploaded (see **Sounds** in the menu, where you can listen to each).
**Keep playing until it is closed** turns it into an alarm that stops the
moment the person closes or answers the alert.

---

## Designing how it looks

**Look and buttons** changes the alert's appearance. The preview on the right
updates as you go.

- **Icon:** a symbol beside the title, such as a bell, a calendar or a fire.
- **Colour:** the band across the top (and the whole background of a
  full-screen alert). *Auto* uses the colour for the level you chose.
- **Label in the coloured band:** replaces *Notice / Warning / Urgent*, for
  example *IT team* or *Exams office*.
- **Close button wording:** for example *Got it* instead of *Dismiss*.
- **Reply buttons:** ask a question. Add up to four replies, such as
  *I'm safe* / *I need help*. The alert stays up until each person picks one,
  and **Sent alerts** shows a live tally and who chose what. This is useful for
  a fire-drill roll call or "Can you cover period 3?".

---

## Sending later

Under **When**, choose **Schedule for later**:

- **Once:** pick a date and time.
- **Every week:** tick the days and set a time. For example "Save your work"
  at 15:50 every weekday.

The **Scheduled** page lists everything that's waiting, when it next goes out,
and what happened last time. You can **Pause**, **Edit**, **Send now** or
**Delete** your own schedules. A schedule sends as you, to the people you
chose. If the server was switched off at the moment one was due, that run is
skipped rather than sent late.

---

## Locking screens

If your account is allowed (administrators always are), **Lock screens** in
the menu locks a room's computers:

1. Choose the computers, the same way as for an alert.
2. Choose how:
   - **Blank screen** covers every monitor with your message.
   - **Freeze** leaves the screen visible, for example while you talk over a
     presentation, and blocks only keyboard and mouse.
3. Write a message, choose a **safety limit**, and press **Lock screens**.

Locked rooms appear under **Locked now** with how many PCs are locked.
**Unlock** lifts one lock; **Unlock everything** (or **Unlock all mine**) lifts
them all. Every lock also lifts itself at the safety limit, even if the PC
can't reach the server.

Good to know:

- Alerts still appear above a locked screen and can be answered.
- Windows always lets someone press **Ctrl+Alt+Del** and sign out. If they
  sign back in while the lock is on, it comes straight back.

---

## Templates

If you send the same kind of message often, fill in the form once and press
**Save as a template**. Templates appear as buttons at the top of the Send
page. Click one to fill in the form, adjust anything you like, then choose the
audience and send.

A template remembers the wording and the appearance. It deliberately does
**not** remember the audience — choosing who receives an alert should always be
something you do on purpose.

To delete a template, click the **×** next to its name.

---

## Seeing what happened, and taking an alert back

**Sent alerts** lists recent alerts, newest first. Click one to see:

- how many computers it was sent to and how many actually displayed it;
- if you asked for confirmation: who has confirmed and who has not;
- a line per computer — which user was logged on, when it was shown, when it was closed.

While an alert is *Still delivering* you can press **Recall**. It vanishes from
every screen still showing it, and stops being delivered to anyone new. You can
recall your own alerts; administrators can recall anyone's.

**Send again…** copies an old alert back into the Send form.

---

## Writing a good alert

- **Say what to do**, not just what is happening. "Save your work and log off by 3:55" beats "The room is closing".
- **Lead with the action.** The title is read; the message often is not.
- **Be brief.** Two sentences is plenty.
- **Include a time** if there is one. The alert shows when it was sent, but "in 5 minutes" is ambiguous to someone who reads it 4 minutes late — "at 3:55" is not.
- **One alert per thing.** Several at once stack up and get closed unread.

## Questions

**Can students reply?** No. Alerts go one way.

**Can I send to a computer nobody is logged on to?** No — there is nobody to show it to. With "show it to people who log on later" it will appear if someone logs on in time.

**I sent it to the wrong people.** Open **Sent alerts** and press **Recall** straight away.

**Does it work on staff laptops at home?** Only while they can reach the Foghorn server, which normally means on site or on the VPN.

**I forgot my password.** Ask your Foghorn administrator to reset it.
