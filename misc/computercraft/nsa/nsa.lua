-- util
function string.starts(String,Start)
    return string.sub(String,1,string.len(Start))==Start
 end

function dump(o)
    if type(o) == 'table' then
        local s = '{ '
        for k,v in pairs(o) do
            if type(k) ~= 'number' then k = '"'..k..'"' end
            s = s .. '['..k..'] = ' .. dump(v) .. ','
        end
        return s .. '} '
    else
        return tostring(o)
    end
end

-- app funcs
function listenChannelBlock(modem, from, to)
    for i = from, to do
        print("Opening channel " .. i)
        modem.open(i)
    end
end

-- main
local modems = { peripheral.find("modem", function(name, modem)
    modem.closeAll()
    return modem.isWireless() -- Check this modem is wireless.
end) }

if #modems == 0 then
    error("No wireless modems found.")
end

listenChannelBlock(modems[1], 0, 127)
listenChannelBlock(modems[2], 65408, 65535)

while true do
    -- local event, modemSide, senderChannel, replyChannel, message, senderDistance = os.pullEvent("modem_message")
    local event = {os.pullEvent("modem_message")}

    -- -- handle nil sender distance
    -- if senderDistance == nil then
    --     senderDistance = 0
    -- end

    -- jsonify message
    local msgJSON = textutils.serialiseJSON(event)
    local msgSize = string.len(msgJSON)
    print("Got message, size: " .. msgSize)

    -- post to server
    local url = "https://8bb3-2601-600-817f-a197-7cb0-ac19-1309-18b6.ngrok.io/event"
    http.post(url, msgJSON, {["Content-Type"] = "application/json"})

    -- print("Received message on channel " .. senderChannel .. ", reply channel " .. replyChannel .. "from " .. senderDistance .. " blocks away, msg size: " .. msgSize)
end
