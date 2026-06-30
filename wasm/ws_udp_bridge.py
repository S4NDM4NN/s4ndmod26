#!/usr/bin/env python3
import argparse
import asyncio
import socket

import websockets
from websockets.exceptions import ConnectionClosed


async def bridge_connection(websocket, target_host: str, target_port: int) -> None:
    loop = asyncio.get_running_loop()
    udp = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    udp.setblocking(False)
    target = (target_host, target_port)

    peer = websocket.remote_address
    print(f"ws-udp: accepted websocket from {peer}, forwarding to {target_host}:{target_port}", flush=True)
    port_frame = b"\xff\xff\xff\xffport" + bytes([(target_port >> 8) & 0xFF, target_port & 0xFF])
    await websocket.send(port_frame)
    print(f"ws-udp: sent control frame server port={target_port}", flush=True)

    async def ws_to_udp() -> None:
        async for message in websocket:
            if isinstance(message, str):
                message = message.encode("utf-8")
            if (
                len(message) == 10
                and message[:8] == b"\xff\xff\xff\xffport"
            ):
                continue
            await loop.sock_sendto(udp, message, target)

    async def udp_to_ws() -> None:
        while True:
            payload = await loop.sock_recv(udp, 65535)
            if not payload:
                return
            await websocket.send(payload)

    try:
        await asyncio.gather(ws_to_udp(), udp_to_ws())
    except ConnectionClosed:
        pass
    finally:
        udp.close()
        print(f"ws-udp: closed websocket from {peer}", flush=True)


async def main() -> None:
    parser = argparse.ArgumentParser(description="Bridge WebSocket clients to a UDP target.")
    parser.add_argument("--listen-host", default="0.0.0.0")
    parser.add_argument("--listen-port", type=int, default=27960)
    parser.add_argument("--target-host", required=True)
    parser.add_argument("--target-port", type=int, required=True)
    args = parser.parse_args()

    async with websockets.serve(
        lambda ws: bridge_connection(ws, args.target_host, args.target_port),
        args.listen_host,
        args.listen_port,
        max_size=None,
        ping_interval=None,
        subprotocols=["binary"],
    ):
        print(
            f"ws-udp: listening on {args.listen_host}:{args.listen_port}, "
            f"proxying to udp://{args.target_host}:{args.target_port}",
            flush=True,
        )
        await asyncio.Future()


if __name__ == "__main__":
    asyncio.run(main())
