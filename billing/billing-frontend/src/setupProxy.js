const proxy = require('http-proxy-middleware');

module.exports = function(app) {
    app.use(proxy('/api', {
        target: 'http://localhost:5000/',
        pathRewrite: {'^/api' : ''}
    }));

    app.use(proxy('/swagger**', {
        target: 'http://localhost:5000/',
    }));
};
