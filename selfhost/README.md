# Hosting your own s4ndmod26 server

One container runs the game server and **s4nd-agent**, which starts and watches the game server, serves its info,
replays and pk3 downloads, and lets browser players connect.  Everything the server keeps lives in `./server`.

## Start

```sh
cp .env.example .env      # then edit it
docker compose up -d
docker compose logs -f server
```

On first start the agent writes `server/server.cfg`. That file is yours from then on (name, password, maps, bots)
and is never overwritten. It contains a random rcon password unless you set `RCON_PASSWORD` in `.env` first.
Restart the server after editing it: `docker compose restart server`.

Forward these ports on your router or firewall:

| Port | Protocol | For |
|---|---|---|
| 27960 (`GAME_PORT`) | UDP | players |
| 27970 (`AGENT_PORT`) | TCP | server info and pk3 downloads (not needed with the `https` profile) |
| 80 and 443 | TCP | only with the `https` profile |

Check it: `curl http://localhost:27970/v1/info`.

## What is in `./server`

| Path | What |
|---|---|
| `server.cfg` | your config |
| `replays/`, `stats/` | recorded games and per-game stats |
| `.agent/identity.pem` | the server's identity (its id in `/v1/info`). **Keep it, and keep it private**: it is how s4ndmod.com will recognise your server once it has been added to the server list. Back it up with the rest of the folder. |
| `s4ndmod26_<version>.pk3`, `qagame…`, `latest.txt` | copied from the image on every start; do not edit |

## Players on the desktop game

That works as soon as UDP 27960 is reachable. To make the mod's pk3 download quickly instead of through the slow
in-game download, set `sv_dlURL` in `server.cfg` to somewhere that serves `s4ndmod26/<pk3 name>`:

- the agent itself: `http://<your address>:27970/dl`, or `https://<DOMAIN>/dl` with the `https` profile;
- or any file host (a bucket, a CDN, a web server) holding a copy of the pk3 from `server/`.

## Players in the browser (s4ndmod.com/play)

Browsers on s4ndmod.com can only connect over HTTPS, so browser players need two things:

1. **The connection to your server:** `wss://<DOMAIN>/ws`. Use the `https` profile:

   - Set `DOMAIN` and `PUBLIC_URL=https://<DOMAIN>` in `.env`.
   - Point the DNS name at this machine and open ports 80 and 443.
   - Run `docker compose --profile https up -d`.

   Caddy gets the certificate and renews it. If you already run a reverse proxy, send `https://<your name>/` to
   port 27970 instead (it must pass WebSocket upgrades).

2. **The mod's pk3:**

   - Servers running an **official release** don't need to do anything: the browser gets the pk3 from s4ndmod.com.
   - A modified server's pk3 comes from its `sv_dlURL`, which must then be **HTTPS** and allow s4ndmod.com to read
     it (CORS: `Access-Control-Allow-Origin`). The agent's `/dl` already does that.

Without HTTPS, your server still works for everyone on the desktop game. The browser client says it can only be
joined from the desktop game.

## Updating

`docker compose pull && docker compose up -d`. The new image's pk3 and game code replace the old ones in `./server`
at start; `server.cfg`, replays, stats and the identity are kept. Pin a version in `S4ND_SERVER_IMAGE` to choose
when you update.
