--
-- reliable_udp.lua — Wireshark dissector for github.com/chvojkav/reliable-udp
--
-- Decodes the fixed 16-byte base header defined in docs/SPEC.md §7.
-- Authored from the spec; verify against a live capture and adjust if needed
-- (see docs/SETUP.md §5). This MUST stay in sync with the `frame` package layout.
--
-- Install: copy into Wireshark's Personal Lua Plugins folder
--   (Help -> About Wireshark -> Folders), then Analyze -> Reload Lua Plugins.
--
-- The dissector registers on UDP port 9000 by default. Change default_port below,
-- or use right-click -> "Decode As..." -> RELUDP for other ports.
--

local default_port = 9000

local proto = Proto("reludp", "Reliable UDP (chvojkav/reliable-udp)")

-- Header fields ------------------------------------------------------------
local f_seq      = ProtoField.uint32("reludp.seq",        "SeqNum",      base.DEC)
local f_ack      = ProtoField.uint32("reludp.ack",        "AckNum",      base.DEC)
local f_window   = ProtoField.uint16("reludp.window",     "Window",      base.DEC)
local f_flags    = ProtoField.uint8 ("reludp.flags",      "Flags",       base.HEX)
local f_syn      = ProtoField.bool  ("reludp.flags.syn",  "SYN",         8, nil, 0x01)
local f_ack_flag = ProtoField.bool  ("reludp.flags.ack",  "ACK",         8, nil, 0x02)
local f_fin      = ProtoField.bool  ("reludp.flags.fin",  "FIN",         8, nil, 0x04)
local f_rst      = ProtoField.bool  ("reludp.flags.rst",  "RST",         8, nil, 0x08)
local f_doff     = ProtoField.uint8 ("reludp.dataoffset", "DataOffset",  base.DEC)
local f_plen     = ProtoField.uint16("reludp.payloadlen", "PayloadLen",  base.DEC)
local f_resv     = ProtoField.uint16("reludp.reserved",   "Reserved",    base.HEX)
local f_options  = ProtoField.bytes ("reludp.options",    "Options")
local f_payload  = ProtoField.bytes ("reludp.payload",    "Payload")

proto.fields = {
    f_seq, f_ack, f_window, f_flags,
    f_syn, f_ack_flag, f_fin, f_rst,
    f_doff, f_plen, f_resv, f_options, f_payload,
}

local HEADER_LEN = 16

-- Build a compact flag summary like "[SYN, ACK]" for the Info column.
local function flag_summary(flags)
    local parts = {}
    if bit.band(flags, 0x01) ~= 0 then parts[#parts + 1] = "SYN" end
    if bit.band(flags, 0x02) ~= 0 then parts[#parts + 1] = "ACK" end
    if bit.band(flags, 0x04) ~= 0 then parts[#parts + 1] = "FIN" end
    if bit.band(flags, 0x08) ~= 0 then parts[#parts + 1] = "RST" end
    if #parts == 0 then return "[]" end
    return "[" .. table.concat(parts, ", ") .. "]"
end

function proto.dissector(buf, pinfo, tree)
    local len = buf:len()
    if len < HEADER_LEN then
        return 0  -- not ours / truncated; let other dissectors try
    end

    pinfo.cols.protocol = "RELUDP"

    local subtree = tree:add(proto, buf(0, len))

    local seq    = buf(0, 4):uint()
    local ack    = buf(4, 4):uint()
    local window = buf(8, 2):uint()
    local flags  = buf(10, 1):uint()
    local doff   = buf(11, 1):uint()
    local plen   = buf(12, 2):uint()

    subtree:add(f_seq,    buf(0, 4))
    subtree:add(f_ack,    buf(4, 4))
    subtree:add(f_window, buf(8, 2))

    local flags_tree = subtree:add(f_flags, buf(10, 1))
    flags_tree:append_text("  " .. flag_summary(flags))
    flags_tree:add(f_syn,      buf(10, 1))
    flags_tree:add(f_ack_flag, buf(10, 1))
    flags_tree:add(f_fin,      buf(10, 1))
    flags_tree:add(f_rst,      buf(10, 1))

    subtree:add(f_doff, buf(11, 1))
    subtree:add(f_plen, buf(12, 2))
    subtree:add(f_resv, buf(14, 2))

    -- Sanity checks surfaced as expert info rather than hard failures.
    if doff < HEADER_LEN then
        subtree:add_expert_info(PI_MALFORMED, PI_WARN,
            "DataOffset < 16 (header underflow)")
        doff = HEADER_LEN
    end
    if doff > len then
        subtree:add_expert_info(PI_MALFORMED, PI_WARN,
            "DataOffset beyond captured length")
        doff = len
    end

    -- Options occupy [16, doff); present only once options are added (post-v1).
    if doff > HEADER_LEN then
        subtree:add(f_options, buf(HEADER_LEN, doff - HEADER_LEN))
    end

    -- Payload occupies [doff, doff + plen).
    local avail = len - doff
    if plen > 0 and avail > 0 then
        local show = plen
        if show > avail then show = avail end
        subtree:add(f_payload, buf(doff, show))
    end

    -- Info column: seq/ack/win + flags, the at-a-glance protocol view.
    pinfo.cols.info = string.format(
        "Seq=%d Ack=%d Win=%d Len=%d %s",
        seq, ack, window, plen, flag_summary(flags))

    return len
end

-- Register on the default UDP port; users can also "Decode As..." onto others.
local udp_port = DissectorTable.get("udp.port")
udp_port:add(default_port, proto)
