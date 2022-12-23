(function () {
    const socket = new WebSocket("ws://localhost:8888/ws");

    socket.onconnect = () => {
        console.log("Connected!");
    };

    socket.onmessage = (message) => {
        console.log(message);
    };
})();
