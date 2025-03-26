#! /usr/bin/env bash

tmux kill-session -t debug-watch 2>/dev/null
tmux new-session -d -s debug-watch

# split the window into 4 panes like 
# ┌────────────┬────────────┐
# │            │            │
# │    CURL    │    fake    │
# │            │    server  │
# ├─────────────────────────┤
# │                         │
# │         air server      │
# │                         │
# ├─────────────────────────┤
# │                         │
# │         air client      │
# │                         │
# └─────────────────────────┘

tmux split-window -v -p 80
tmux select-pane -t 1
tmux split-window -h
tmux select-pane -t 3
tmux split-window -v -p 50


# Run the fake server in the top right pane
tmux send-keys -t 2 "go run ./scripts/fake.go" C-m
tmux send-keys -t 3 "mise air:server" C-m
tmux send-keys -t 4 "mise air:client" C-m
tmux send-keys -t 1 "watch -n 5 timeout 3 curl test.localhost:8080/" C-m


tmux attach-session -t debug-watch