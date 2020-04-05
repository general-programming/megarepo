const proxy = require('http-proxy-middleware');

module.exports = (app) => {
    app.use(proxy('/api', {
        target: 'https://payapi.catgirls.dev/',
        pathRewrite: {
            '^/api': '',
        },
        changeOrigin: true,
        autoRewrite: true,
        secure: false,
    }));

    app.use(proxy('/swagger**', {
        target: 'http://localhost:5000/',
    }));
};
