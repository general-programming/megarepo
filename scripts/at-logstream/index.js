const io = require("socket.io-client");
const filesize = require("filesize");

let addLog = (msg)  => {
    var itemText = msg.items.length > 1 ? msg.items.length + ' items' : msg.item;
    var size = filesize(msg.bytes, {standard: 'iec', unix: false});

    console.log(`[${msg.downloader}@${msg.project}] ${itemText}, ${size}`);
}

let createSocket = (url) => {
    let socket = io.connect(url);

    socket.on("connect", () => {
        console.log("connected", socket.id, url);
    });

    socket.on("disconnect", () => {
        console.log("disconnected", url);
    });

    socket.on("connect_error", () => {
        console.log("connection error", url);
    });

    socket.on("log_message", (data) => {
        var msg = JSON.parse(data);
        // console.log(msg);
        if (msg.downloader && msg.item && msg.bytes !== undefined) {
            addLog(msg);
        }
    });
};
