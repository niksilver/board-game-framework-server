# Board game framework

A simple framework for building networked board games. All the game
intelligence is in the clients; the server simply provides the
communication.

Clients (players) connect to a server using websockets.
Each one joins a group, which is an instance of a shared game.
Clients are expected to maintain the state of the game.
All the server does is bounce any incoming message from a sending
client to all the other clients in the same game (instance).
Thus a message may be saying "This is my move", or "Please give me an
up to date state of the game", or anything else.

Inspired by the [open source version of Codenames](https://github.com/jbowens/codenames/).

This repo holds just the server code. Documentation, plus the client code
(for Elm and a bit of JavaScript), and examples are at
[https://github.com/niksilver/board-game-framework].
