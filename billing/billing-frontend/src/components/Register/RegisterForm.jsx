/* eslint-disable camelcase */
import React from 'react';
import PropTypes from 'prop-types';
import Avatar from '@material-ui/core/Avatar';
import Button from '@material-ui/core/Button';
import CssBaseline from '@material-ui/core/CssBaseline';
import Input from '@material-ui/core/Input';
import InputLabel from '@material-ui/core/InputLabel';
import Paper from '@material-ui/core/Paper';
import FormControl from '@material-ui/core/FormControl';
import Typography from '@material-ui/core/Typography';
import withStyles from '@material-ui/core/styles/withStyles';
import { withRouter } from 'react-router-dom';
import { connect } from 'react-redux';

import compactLogo from '../../assets/img/logo-square-compact.png';

import validateRegisterInput from '../../validation/register';
import { register } from '../../actions/user.action';

const styles = theme => ({
    main: {
        width: 'auto',
        display: 'block', // Fix IE 11 issue.
        marginLeft: theme.spacing.unit * 3,
        marginRight: theme.spacing.unit * 3,
        [theme.breakpoints.up(400 + theme.spacing.unit * 3 * 2)]: {
            width: 400,
            marginLeft: 'auto',
            marginRight: 'auto',
        },
    },
    paper: {
        marginTop: theme.spacing.unit * 8,
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        padding: `${theme.spacing.unit * 2}px ${theme.spacing.unit * 3}px ${theme.spacing.unit * 3}px`,
    },
    avatar: {
        margin: theme.spacing.unit,
        backgroundColor: theme.palette.primary.light,
    },
    form: {
        width: '100%', // Fix IE 11 issue.
        marginTop: theme.spacing.unit,
    },
    submit: {
        marginTop: theme.spacing.unit * 3,
    },
    button: {
        marginTop: theme.spacing.unit * 3,
    },
});

class RegisterForm extends React.Component {
    constructor(props) {
        super(props);

        this.state = {
            email: '',
            password: '',
            password_verify: '',
            username: '',
            errors: {},
        };

        this.handleChange = this.handleChange.bind(this);
        this.handleSubmit = this.handleSubmit.bind(this);
    }

    handleChange(e) {
        const { name, value } = e.target;
        this.setState({ [name]: value });
    }

    handleSubmit(e) {
        e.preventDefault();

        const {
            email, username, password, password_verify,
        } = this.state;

        const data = {
            email,
            username,
            password,
            password_verify,
        };

        const { dispatch, history } = this.props;

        const validation = validateRegisterInput(data);
        if (!validation.isValid) {
            this.setState({
                errors: validation.errors,
            });
            console.log(validation.errors);
            return;
        }

        dispatch(register(data));
    }

    render() {
        const { classes, registering } = this.props;
        const {
            email, username, password, password_verify, errors,
        } = this.state;

        return (
            <main className={classes.main}>
                <CssBaseline />
                <Paper className={classes.paper}>
                    <Avatar src={compactLogo} className={classes.avatar} />
                    <Typography component="h1" variant="h5">
                        Register
                    </Typography>
                    <form className={classes.form} onSubmit={this.handleSubmit}>
                        <FormControl margin="normal" required fullWidth>
                            <InputLabel htmlFor="email">Email Address</InputLabel>
                            <Input
                                id="email"
                                name="email"
                                autoComplete="email"
                                autoFocus
                                value={email}
                                onChange={this.handleChange}
                                error={!!errors.email}
                            />
                        </FormControl>
                        <FormControl margin="normal" required fullWidth>
                            <InputLabel htmlFor="username">Desired Username</InputLabel>
                            <Input
                                name="username"
                                id="username"
                                autoComplete="username"
                                value={username}
                                onChange={this.handleChange}
                                error={!!errors.username}
                            />
                        </FormControl>
                        <FormControl margin="normal" required fullWidth>
                            <InputLabel htmlFor="password">Password</InputLabel>
                            <Input
                                name="password"
                                type="password"
                                id="password"
                                autoComplete="current-password"
                                value={password}
                                onChange={this.handleChange}
                                error={!!errors.password}
                            />
                        </FormControl>
                        <FormControl margin="normal" required fullWidth>
                            <InputLabel htmlFor="password_verify">Retype Password</InputLabel>
                            <Input
                                name="password_verify"
                                type="password"
                                id="password_verify"
                                autoComplete="current-password"
                                value={password_verify}
                                onChange={this.handleChange}
                                error={!!errors.password_verify}
                            />
                        </FormControl>
                        <Button
                            type="submit"
                            fullWidth
                            variant="contained"
                            color="primary"
                            className={classes.submit}
                            disabled={registering}
                        >
                            Register
                        </Button>
                    </form>
                </Paper>
            </main>
        );
    }
}

function mapStateToProps(state) {
    const { registering } = state.registration;
    return {
        registering,
    };
}


RegisterForm.propTypes = {
    classes: PropTypes.object.isRequired,
    registering: PropTypes.bool,
    dispatch: PropTypes.func,
};

export default connect(mapStateToProps)(withStyles(styles)(withRouter(RegisterForm)));
