# Deploy

The `docker compose` topology the end-to-end tests run against (PLAN §9):
Postgres, this server, a **real** Hoplock Proxy, an sshd target, and a client
image running scenario SSH clients.

The point of it is the thing neither repository can test alone — a real proxy
driven by a real PDP. The Hoplock Proxy repository proves it enforces what a
mock tells it; this repository proves it decides correctly in isolation; only
here do "decides" and "enforces" meet.

Built in **phase 0016**.
