
class Garlicoin(Coin):
    NAME = "Garlicoin"
    SHORTNAME = "GRLC"
    NET = "mainnet"
    XPUB_VERBYTES = bytes.fromhex("0488b21e")
    XPRV_VERBYTES = bytes.fromhex("0488ade4")
    P2PKH_VERBYTE = bytes.fromhex("26")
    P2SH_VERBYTES = [bytes.fromhex("32"), bytes.fromhex("05")]
    WIF_BYTE = bytes.fromhex("b0")
    GENESIS_HASH = ('2ada80bf415a89358d697569c96eb98c'
                    'dbf4c3b8878ac5722c01284492e27228')
    DESERIALIZER = lib_tx.DeserializerSegWit
    TX_COUNT = 1
    TX_COUNT_HEIGHT = 1
    TX_PER_BLOCK = 10
    RPC_PORT = 9332
    REORG_LIMIT = 800
    PEERS = [
        'ske.wtf s50004 t50003',
        '172.93.54.31 s t',
        '84.245.14.110 s t',
    ]
