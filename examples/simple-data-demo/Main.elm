-- Copyright 2020 Nik Silver
--
-- Licensed under the GPL v3.0. See file LICENCE.txt for details.


port module Main exposing (..)


import Browser
import Html exposing (..)
import Html.Attributes as Attr
import Html.Events as Events
import Json.Encode as Enc


main =
  Browser.element
  { init = init
  , update = update
  , subscriptions = subscriptions
  , view = view
    }


-- Null model and initialisation


type alias Model =
  { draftGameID: String
  }


init : () -> (Model, Cmd Msg)
init _ =
  ( {draftGameID = "sample-game-id"}
  , Cmd.none
  )


-- Update the model with a message


type Msg =
  GameID String
  | OpenClick
  | Received String


update : Msg -> Model -> (Model, Cmd Msg)
update msg model =
  case msg of
    GameID id ->
      ( { model | draftGameID = id }
      , Cmd.none
      )

    OpenClick ->
      (model, Open model.draftGameID |> encode |> outgoing)

    Received _ ->
      (model, Cmd.none)


-- Subscriptions


subscriptions : Model -> Sub Msg
subscriptions model =
  incoming toMessage


-- Ports to communicate with the framework


port outgoing : Enc.Value -> Cmd msg
port incoming : (Enc.Value -> msg) -> Sub msg


type Request =
  Open String


-- Turn an application request into something that can be sent out
-- through a port

encode : Request -> Enc.Value
encode req =
  case req of
    Open gameID ->
      Enc.object
        [ ("instruction", Enc.string "Open")
        , ("url", "ws://localhost:8080/g/" ++ gameID |> Enc.string)
        ]


-- Turn something that's come in from a port into a message we can
-- do something about.

toMessage : Enc.Value -> Msg
toMessage v =
  Enc.encode 0 v
  |> Debug.log "Incoming message"
  |> Received


-- View


view : Model -> Html Msg
view model =
  div[]
    [ p [] [text """
        Choose a game ID,
        then click "Open" to connect to the server.
        "Send" to send a message to the server.
        "Close" to close the connection. 
        You can change the message and send multiple times.
        """]
    , p [] [text "This code assumes the server is at http://localhost:8080"]
    , p []
      [ text "http://localhost:8080/g/"
      , input
        [ Attr.id "gameid"
        , Attr.type_ "text"
        , Attr.value model.draftGameID
        , Events.onInput GameID
        ] []
      , button
        [ Attr.id "open"
        , Events.onClick OpenClick
        ] [ text "Open" ]
      ]
    ]
