from pyinfra import host, local

local.include("tasks/setup_holepunch.py")
# local.include("tasks/setup_docker_v6.py")
local.include("tasks/setup_consul_template.py")
local.include("tasks/setup_vault_agent.py")
local.include("tasks/setup_nomad.py")
