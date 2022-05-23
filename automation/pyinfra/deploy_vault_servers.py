from pyinfra import host, local

local.include("tasks/setup_holepunch.py")
local.include("tasks/setup_consul_template.py")
local.include("tasks/setup_consul.py")
