const proxy = require('http-proxy-middleware');

module.exports = (app) => {
    app.use(proxy('/api', {
        target: 'https://pay.generalprogramming.org/api/',
        pathRewrite: {
            '^/api': '',
        },
        changeOrigin: true,
        secure: false,
    }));

    app.use(proxy('/swagger**', {
        target: 'http://localhost:5000/',
    }));
};
