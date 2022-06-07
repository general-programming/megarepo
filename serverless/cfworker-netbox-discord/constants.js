const INTERESTING_FIELDS = {
    serial: 'Serial',
    asset_tag: 'Asset Tag',
    description: 'Description',
    label: 'Label',
    vcpus: 'vCPUs',
    memory: 'Memory',
    address: 'IP Address',
    mac_address: 'MAC',
    model: 'Model',
    cid: 'Circuit ID',
    ssid: 'SSID',
    xconnect_id: 'Cross Connect',
    pp_info: 'Patch Panel Info',
};

const DISPLAY_FIELDS = {
    device: 'Device',
    type: 'Type',
    vlan: 'VLAN',
    site: 'Site',
    circuit: 'Circuit',
    provider: 'Provider',
    vrf: 'VRF',
    mode: 'Mode',
    device_type: 'Device Type',
    auth_type: 'Auth Type',
    auth_cipher: 'Auth Cipher',
    vid: 'VID',
    group: 'Group',
    role: 'Role',
    scope: 'Scope',
    termination_a: 'Termination A',
    termination_z: 'Termination Z',
    installed_device: 'Installed Device',
};

const LABEL_FIELDS = {
    airflow: 'Airflow',
    mode: 'Mode',
};

const COLOR_MAX = 16777215;

export { INTERESTING_FIELDS, DISPLAY_FIELDS, LABEL_FIELDS, COLOR_MAX };
