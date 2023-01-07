-- global state vars
neuralModules = peripheral.wrap("back")
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

function postBlock(x, y, z, obstructed, turtlePath, farmFiducial)
    local url = "https://f964-2601-600-817f-a197-813-f6b2-4844-bdaa.ngrok.io/event"
    local data = {
        x = x,
        y = y,
        z = z,
        obstructed = obstructed,
        turtlePath = turtlePath,
        farmFiducial = farmFiducial,
    }

    socket.send(textutils.serializeJSON(data))
end

function scan()
    local canvas3d = neuralModules.canvas3d()
    canvas3d.clear()
    local canvas = canvas3d.create()

    while true do
        canvas.clear()
        canvas = canvas3d.create()

        local gpsX, gpsY, gpsZ = gps.locate()
        -- get gps cords as whole numbers

        local blocks = neuralModules.scan()
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
                        farmFiducial
                    )
                    local box = canvas.addBox(x-.5, y-.5, z-.5, 1, 1, 1, color)
                    box.setDepthTested(false)
                    -- sentBlocks[trackingKey] = true
                end
            end
        end
        sleep(1)
    end
end

local status, err = pcall(scan)
if not status then
    print(err)
end

socket.close()
