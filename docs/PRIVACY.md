# Privacy at Talkie

Last updated: 8 November 2026

[Deutsche Fassung](DATENSCHUTZ.md)

Talkie is a voice and text chat for a closed group of friends and family.

## The short version

- **We do not run a Talkie server.** There is no public server operated by us.
- **Every Talkie server is run by someone themselves** – usually a family or a group of friends on
  their own hardware.
- **All data stays with the operator of that server.** The app only talks to the server you entered,
  or the one you got your invite code from.
- **We receive no data** – neither from the app nor from any Talkie server.
- No ads, no tracking, no analytics or crash reporting services.

## Who is responsible for what?

**For the app** (publisher):

Manuel Weiser\
Wehrmannstr. 3\
04157 Leipzig\
Germany\
Email: manuel.weiser@me.com

As the publisher, we only provide the software. We do not process any personal data about you.

**For the server you are connected to:** the operator of that server – the person who gave you the
invite code. They decide what is stored there and for how long, and they are your contact for any
questions about your data.

## What does the app store on your device?

- your display name and the server address
- a device key that the app creates itself (so the server recognises you without a password)
- your settings (e.g. volume, noise filter, who you have muted)

This data stays on your device. Only your name and the public part of the device key are sent to
the server you entered.

## What is stored on a Talkie server?

This is how the Talkie server software works by default. The operator can change this.

| What | Why |
|---|---|
| **Display name** | so the others can see who is talking |
| **Public device key** | so the server recognises you |
| **Chat messages** | so the others in the room can read them – deleted after 24 hours, and at most the last 200 per room |
| **IP address** | for the connection, troubleshooting and to prevent abuse; it is written to the server log when you connect |

**Voice** is passed on in real time to the others in the same room and is **not recorded**.

The connection between app and server is encrypted (TLS 1.3).

Who can see what:

- The others in the same room see your name and your chat messages and can hear you.
- The server operator can see names, rooms and chat messages in order to step in when there are
  problems (e.g. remove or ban someone).

## Microphone

The app needs the microphone so your friends can hear you. It is only used while you are connected
to a server. You can mute it at any time with the „Mikro an“ (mic on) button.

## App Store and Google Play

If you download Talkie from the App Store or Google Play, Apple or Google process data according to
their own privacy policies. We have no influence on this and we receive no data about you from it.

## Deletion and your rights

- In the app, **„Mehr“/„Einstellungen“ (More/Settings) → „Gerät abmelden“ (sign out device)** deletes the device key and your sign-in on your device.
  If you delete the app, all data on the device is gone as well.
- Your data on a server is deleted by the **server operator**. Please contact them.

Your rights to access, rectification, erasure, restriction, data portability and objection are
directed at the respective server operator. As we do not hold any data about you, we cannot provide
any information about it. You can also lodge a complaint with a data protection supervisory
authority.

## Children

Talkie is meant for children within a circle of friends and family. Only people invited by the
server operator can join. Parents with questions should contact the operator of their server.

## Note for server operators

If you run a Talkie server, you are responsible for the data on that server. By default the server
software stores names, device keys and chat messages (deleted after 24 hours, at most the last 200
per room; set with `TALKIE_CHAT_HOURS`) and writes the name and IP address to the log when someone
connects. Decide how long the log is kept, and inform your users or their parents.
