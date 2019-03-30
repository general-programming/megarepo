const inquirer = require("inquirer");
const parser = require("set-cookie-parser");

class login {
    constructor(base) {
        this.__base = base;
    }
    start() {
        return new Promise(async (resolve, reject) => {
            try {
                let answers = await inquirer.prompt([
                    {
                        type: "input",
                        name: "username",
                        message: "Please enter your username"
                    },
                    {
                        type: "password",
                        name: "password",
                        mask: "*",
                        message: "Please enter your password"
                    }
                ]);
                let response = await this.__base.__swaggerAPI.apis.account.post_account_login_resource(answers);

                let cookies = parser(response.headers["set-cookie"], { decodeValues: false, map: true });
                
                resolve({ cookie: cookies["gpbilling"].value, userObj: response.obj });
            } catch (error) {
                reject(error);
            }
        });
    }
}

module.exports = login;
