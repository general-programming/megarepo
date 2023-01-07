-- global state vars
socket, failReason = http.websocket("wss://f964-2601-600-817f-a197-813-f6b2-4844-bdaa.ngrok.io/eventpush")
if not socket then
    error("Failed to open websocket: " .. failReason)
end
local sentBlocks = {}

-- overhead blocks that dictate the allowed path of the robot
local pathFiducials = {
    -- path fiducials
    ["minecraft:glowstone"] = true,
    ["minecraft:chiseled_stone_bricks"] = true,
    -- lava miner is ok
    ["minecraft:pointed_dripstone"] = true,
    -- item/fuel points
    ["computercraft:wired_modem_full"] = true,
    ["computercraft:cable"] = true,
}

-- blocks below the turtle for farmland
local farmFiducials = {
    ["minecraft:dirt"] = true,
    ["minecraft:farmland"] = true,
}

-- blocks that are permitted on the turtle y level
local allowedBlocks = {
    ["computercraft:turtle_normal"] = true,
    ["minecraft:air"] = true,
}

function getBlockFromScan(blocks, x, y, z)
    local radius = math.floor((#blocks) ^ (1/3)) / 2
    local width = radius * 2 + 1
    local index = width ^ 2 * (x + radius) + width * (y + radius) + (z + radius) + 1
    return blocks[index]
end

function postBlock(x, y, z, obstructed, turtlePath, farmFiducial, modem)
    local data = {
        block = {
            x = x,
            y = y,
            z = z,
            obstructed = obstructed,
            turtlePath = turtlePath,
            farmFiducial = farmFiducial,
            modem = modem,
        }
    }

    socket.send(textutils.serializeJSON(data))
end

function loadRightModule(name)
    for i = 1, 16 do
        local item = turtle.getItemDetail(i)
        if item and item.name == name then
            turtle.select(i)
            turtle.equipRight()
            return true
        end
    end
    return false
end

function getScanner()
    local scanner = peripheral.find("plethora:scanner")
    if not scanner then
        local loadAttempt = loadRightModule("plethora:module_scanner")
        if not loadAttempt then
            error("No scanner found")
        end
        return getScanner()
    end
    return scanner
end

function scan()
    loadRightModule("computercraft:wireless_modem_normal")
    local gpsX, gpsY, gpsZ = gps.locate()
    local scanner = getScanner()

    reportPosition(gpsX, gpsY, gpsZ)
    -- get gps cords as whole numbers

    local blocks = scanner.scan()
    local radius = math.floor((#blocks) ^ (1/3)) / 2
    local y = 0 -- scan the player head, turtles are assumed to be on the same level.

    for x = -radius, radius do
        for z = -radius, radius do
            -- get true x,y,z
            local trueX = gpsX + x
            local trueY = gpsY
            local trueZ = gpsZ + z

            -- skip blocks we already have processed
            local trackingKey = trueX .. "," .. trueZ
            if not sentBlocks[trackingKey] then
                local block = getBlockFromScan(blocks, x, y, z)
                local blockUp = getBlockFromScan(blocks, x, y + 1, z)
                local blockDown = getBlockFromScan(blocks, x, y - 1, z)
                local blockDownTwo = getBlockFromScan(blocks, x, y - 2, z)

                local obstructed = true
                local turtlePath = false
                local farmFiducial = false
                local modem = false
                local color = 0xff000001

                if allowedBlocks[block.name] then
                    obstructed = false
                    color = 0xff000001
                    -- print("Found turtle block " .. block.name .. " at " .. trueX .. " " .. trueY .. " " .. trueZ)
                    if pathFiducials[blockUp.name] then
                        -- set flag
                        turtlePath = true
                        color = 0xffffff01

                        -- ensure there is air below the turtle and a farm fiducial below the air
                        if blockDown.name == "minecraft:air" and farmFiducials[blockDownTwo.name] then
                            farmFiducial = true
                            color = 0x00000000
                        end

                        if blockUp.name == "computercraft:wired_modem_full" then
                            modem = true
                        end
                    else
                        -- print("No path fiducial " .. blockUp.name .. " at " .. trueX .. " " .. trueY+1 .. " " .. trueZ .. ", bad")
                    end
                else
                    -- print("No turtle block " .. block.name .. " at " .. trueX .. " " .. trueY .. " " .. trueZ .. ", bad")
                end
                postBlock(
                    math.floor(trueX),
                    math.floor(trueY),
                    math.floor(trueZ),
                    obstructed,
                    turtlePath,
                    farmFiducial,
                    modem
                )
            end
        end
    end
end

function fuel()
    if turtle.getFuelLevel() > 1000 then
        return
    end

    for i = 1, 16 do
        turtle.select(i)
        local ok, _ = turtle.refuel()
        if ok then
            print("New fuel level is " .. turtle.getFuelLevel())
        end
    end
    turtle.select(1)
end

function reportPosition(x, y, z)
    local data = {
        turtle = {
            name = os.getComputerID(),
            fuel = turtle.getFuelLevel(),
            maxFuel = turtle.getFuelLimit(),
            x = x,
            y = y,
            z = z,
        }
    }

    socket.send(textutils.serializeJSON(data))
end

function main()
    print("Starting roomba")
    -- turtle.forward()
    fuel()

    local status, err = pcall(scan)
    if not status then
        print(err)
    end

    socket.close()
end

main()
