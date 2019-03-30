const Swagger = require("swagger-client");

const homedir = require("os").homedir();
const fs = require("fs");

let { login, logout, register, products, verify } = require("./index");

class GPBilling {
    constructor() {
        this.__swaggerURL = "https://payapi.catgirls.dev/swagger.json";
        this.__swaggerAPI = null;
        this.billingConfig = {};
        this.login = new login(this);
        this.logout = new logout(this);
        this.register = new register(this);
        this.products = new products(this);
        this.verify = new verify(this);
    }
    
    assignSwaggerAPI() {
        return new Promise(async (resolve, reject) => {
            try {
                // if we have an authCookie on hand
                if (this.billingConfig.authCookie) {
                    // we inject it to the request via the requestInterceptor
                    this.__swaggerAPI = await Swagger(this.__swaggerURL, {
                        requestInterceptor: (request) => {
                            request.headers["cookie"] = this.billingConfig.authCookie;
                            return request;
                        }
                    });
                } else {
                    // else we just go without doing anything
                    this.__swaggerAPI = await Swagger(this.__swaggerURL);
                }
                resolve(true);
            } catch (error) {
                reject(error);
            }
        });
    }

    loadBillingConfig(loadFromFile = true) {
        return new Promise(async (resolve, reject) => {
            try {
                // find seperator for directory
                let sep = (homedir.includes("\\") ? "\\" : "/");
                // ~/.config/.gpbilling.json
                let filePath = `${homedir}${sep}.config${sep}.gpbilling.json`;
                // ~/.config/
                let folderPath = `${homedir}${sep}.config${sep}`;

                let configExists = fs.existsSync(filePath);
                let folderExists = fs.existsSync(folderPath);

                // if we are loading from the config file AND if the config exists
                if (configExists && loadFromFile) {
                    // we require it
                    this.billingConfig = require(filePath);
                } else {
                    // if folder doesn't exists, make it
                    if (!folderExists) fs.mkdirSync(folderPath);
                    // write the .gpbilling.json file.
                    fs.writeFileSync(filePath, JSON.stringify(this.billingConfig, null, 4));
                }
                resolve(true);
            } catch (error) {
                reject(error);
            }
        });
    }

    saveCookie(cookie) {
        return new Promise(async (resolve, reject) => {
            try {
                this.billingConfig.authCookie = `gpbilling=${cookie}`;
                await this.loadBillingConfig(false);
                resolve(true);
            } catch (error) {
                reject(error);
            }
        });
    }

    nukeCookie() {
        return new Promise(async (resolve, reject) => {
            try {
                delete this.billingConfig.authCookie;
                await this.loadBillingConfig(false);
                resolve(true);
            } catch (error) {
                reject(error);
            }
        });
    }

    init() {
        return new Promise(async (resolve, reject) => {
            try {
                await this.loadBillingConfig();
                await this.assignSwaggerAPI();
                resolve(true);
            } catch (error) {
                reject(error);
            }
        });
    }
}

module.exports = GPBilling;