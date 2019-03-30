const inquirer = require("inquirer");
const url = require("url");

class verify {
    constructor(base) {
        this.__base = base;
    }

    askVerifyLink() {
        return new Promise(async (resolve, reject) => {
            try {
                let answer = await inquirer.prompt([{
                    type: "input",
                    name: "url",
                    message: "Please paste the URL you got from the verification mail"
                }]);
                let parsedData = url.parse(answer.url);
                if (parsedData.host !== "pay.generalprogramming.org" || parsedData.pathname !== "/verify" || !parsedData.query || parsedData.query.length !== 36) {
                    console.error("\nInvalid verify link, please enter it again.\n");
                    let q = await this.askVerifyLink();
                    resolve(q);
                } else {
                    resolve(parsedData.query);
                }
            } catch (error) {
                reject(error);
            }
        });
    }

    start() {
        return new Promise(async (resolve, reject) => {
            try {
                let verificationCode = await this.askVerifyLink();

                let resp = await this.__base.__swaggerAPI.apis.account.post_account_verify_resource({ token: verificationCode });
                resolve(resp.obj);
            } catch (error) {
                reject(error);
            }
        });
    }
}

module.exports = verify;
