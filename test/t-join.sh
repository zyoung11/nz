#!/usr/bin/env bash
source "$(dirname "$0")/lib.sh"

info "join flow"

out=$(nzs list --pending 2>&1)
expect_contains "pending shows friend/laptop" "$out" "laptop"
expect_contains "pending shows friend/pc" "$out" "pc"

out=$(nzs list 2>&1)
expect_contains "admin machine auto approved" "$out" "desktop"

nzs allow friend
nzs allow friend/laptop
sleep 2

out=$(nzs list 2>&1)
expect_contains "friend/laptop active" "$out" "active"

wait_joined node-b 10
logs=$(container_log node-b)
expect_contains "node-b saw approval" "$logs" "joined as"

finish
